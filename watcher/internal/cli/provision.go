package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
)

// What one password login can find out about the server PC, so init can fill
// these in instead of asking.
type ServerFacts struct {
	Platform  remote.ServerPlatform
	Interface string
	MAC       string
	Broadcast string

	Containers  []string
	MCPort      int
	RCONEnabled bool

	CanSuspend   bool
	CanHibernate bool
	MemoryGB     int
	WakeOnLAN    remote.WakeOnLANSetting
}

// The interface carrying the default route is the one the magic packet arrives
// on, everything else is read relative to it.
func discoverServer(s *remote.ServerSession) ServerFacts {
	facts := ServerFacts{Platform: s.Platform(), WakeOnLAN: remote.WolUnknown}
	if facts.Platform.Windows {
		discoverWindows(s, &facts)
	} else {
		discoverUnix(s, &facts)
	}
	discoverContainers(s, &facts)
	return facts
}

func discoverUnix(s *remote.ServerSession, facts *ServerFacts) {
	if out, err := s.Run("ip route show default | awk '{print $5; exit}'"); err == nil {
		facts.Interface = remote.FirstLine(out)
	}
	if facts.Interface == "" {
		return
	}

	if out, err := s.Run("cat /sys/class/net/" + remote.ShellQuote(facts.Interface) + "/address"); err == nil {
		if _, parseErr := config.ParseMAC(remote.FirstLine(out)); parseErr == nil {
			facts.MAC = remote.FirstLine(out)
		}
	}
	if out, err := s.Run("ip -o -4 addr show dev " + remote.ShellQuote(facts.Interface) + " | awk '{print $6; exit}'"); err == nil {
		facts.Broadcast = remote.FirstLine(out)
	}

	if out, err := s.Run("awk '/MemTotal/{print $2}' /proc/meminfo"); err == nil {
		facts.MemoryGB = kilobytesToGB(remote.FirstLine(out))
	}

	// /sys/power/state lists what the kernel can actually do, mem is suspend
	// to RAM and disk is hibernate.
	if out, err := s.Run("cat /sys/power/state"); err == nil {
		facts.CanSuspend = strings.Contains(out, "mem")
		facts.CanHibernate = strings.Contains(out, "disk")
	}

	// ethtool usually needs root to report the wake settings.
	if out, err := s.RunSudo("ethtool " + remote.ShellQuote(facts.Interface)); err == nil {
		facts.WakeOnLAN = remote.ParseWakeOnLANSetting(out)
	} else if out, err := s.Run("ethtool " + remote.ShellQuote(facts.Interface)); err == nil {
		facts.WakeOnLAN = remote.ParseWakeOnLANSetting(out)
	}
}

func discoverWindows(s *remote.ServerSession, facts *ServerFacts) {
	if out, err := s.Run("(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | " +
		"Select-Object -First 1).InterfaceAlias"); err == nil {
		facts.Interface = remote.FirstLine(out)
	}
	if facts.Interface == "" {
		return
	}

	if out, err := s.Run("(Get-NetAdapter -Name '" + facts.Interface + "').MacAddress"); err == nil {
		if _, parseErr := config.ParseMAC(remote.FirstLine(out)); parseErr == nil {
			facts.MAC = remote.FirstLine(out)
		}
	}
	if out, err := s.Run(remote.WolStatusCommandWindows); err == nil {
		facts.WakeOnLAN = remote.ParseWakeOnLANSetting(out)
	}
	if out, err := s.Run("(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory"); err == nil {
		facts.MemoryGB = bytesToGB(remote.FirstLine(out))
	}
	// Windows suspend over SSH is unreliable, hibernate is the honest option.
	facts.CanHibernate = true
}

func discoverContainers(s *remote.ServerSession, facts *ServerFacts) {
	if !facts.Platform.HasDocker {
		return
	}
	out, err := s.Run("docker ps -a --format '{{.Names}}'")
	if err != nil {
		return
	}
	for _, name := range strings.Fields(out) {
		if config.ContainerNamePattern.MatchString(name) {
			facts.Containers = append(facts.Containers, name)
		}
	}
}

// Reads the published Minecraft port and whether RCON is on, both of which only
// make sense once a container has been picked.
func inspectContainer(s *remote.ServerSession, name string) (mcPort int, rcon bool) {
	if out, err := s.Run("docker port " + remote.ShellQuote(name) + " 25565/tcp"); err == nil {
		mcPort = parseDockerPort(out)
	}
	if out, err := s.Run("docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' " + remote.ShellQuote(name)); err == nil {
		rcon = parseRCONEnabled(out)
	}
	return mcPort, rcon
}

// docker port answers "0.0.0.0:25565" and often a second line for IPv6.
func parseDockerPort(out string) int {
	_, port, found := strings.Cut(remote.FirstLine(out), ":")
	if !found {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil {
		return 0
	}
	return parsed
}

// itzg/minecraft-server turns RCON on through this environment variable, and
// the sleep monitor has no other way to count players.
func parseRCONEnabled(env string) bool {
	for _, line := range strings.Split(env, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && strings.EqualFold(key, "ENABLE_RCON") {
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return false
}

func kilobytesToGB(value string) int {
	kb, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return int(kb / (1024 * 1024))
}

func bytesToGB(value string) int {
	bytes, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return int(bytes / (1024 * 1024 * 1024))
}

// A quarter of the machine, floored at 2G because less is not worth running and
// capped at 8G because beyond that the garbage collector costs more than the
// heap gains.
func suggestedMemory(totalGB int) string {
	if totalGB <= 0 {
		return "4G"
	}
	suggested := totalGB / 4
	if suggested < 2 {
		suggested = 2
	}
	if suggested > 8 {
		suggested = 8
	}
	return strconv.Itoa(suggested) + "G"
}

// A card left at Wake-on: d swallows the magic packet, which makes this whole
// project do nothing with no other symptom.
func enableWakeOnLAN(s *remote.ServerSession, iface string) error {
	if s.Platform().Windows {
		_, err := s.Run("Set-NetAdapterPowerManagement -Name '" + iface + "' -WakeOnMagicPacket Enabled")
		return err
	}

	unit := wakeOnLANUnit(iface)
	staged, err := remote.StageFile(s, "wol-unit", unit)
	if err != nil {
		return err
	}
	// Set it now and again on every boot, most distributions reset it.
	install := fmt.Sprintf(
		"set -e; ethtool -s %s wol g; install -o root -g root -m 0644 %s %s; "+
			"systemctl daemon-reload; systemctl enable %s",
		remote.ShellQuote(iface), remote.ShellQuote(staged), remote.ShellQuote(wakeOnLANUnitPath(iface)),
		remote.ShellQuote(wakeOnLANUnitName(iface)))

	if out, err := s.RunSudo(install); err != nil {
		return fmt.Errorf("%w: %s", err, logging.Sanitize(out, 200))
	}
	s.Run("rm -f " + remote.ShellQuote(staged))
	return nil
}

func wakeOnLANUnitName(iface string) string {
	return "mcwod-arm@" + iface + ".service"
}

func wakeOnLANUnitPath(iface string) string {
	return "/etc/systemd/system/" + wakeOnLANUnitName(iface)
}

func wakeOnLANUnit(iface string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Arm " + iface + " for Wake-on-LAN, installed by mcwod",
		"After=network-online.target",
		"",
		"[Service]",
		"Type=oneshot",
		"ExecStart=/usr/sbin/ethtool -s " + iface + " wol g",
		"RemainAfterExit=yes",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}
