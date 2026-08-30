package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const eulaURL = "https://aka.ms/MinecraftEULA"

// Sets the Minecraft and backup containers up on the server, either as a fresh
// compose file or as two services added to one that is already there.
func offerContainerSetup(p *prompter, s *ServerSession, cfg *Config, facts ServerFacts) bool {
	fmt.Println("\nYour Minecraft server")
	if !facts.Platform.HasDocker {
		printWarning("Docker is not installed on that PC, so there is nothing to set up yet.")
		printHint("Install Docker there, then run mcwod and pick this again.")
		return false
	}
	if len(facts.Containers) > 0 {
		printHint("That PC already runs " + cfg.Server.ContainerName + ".")
		if !p.yesNo("Set up another server anyway?", false) {
			return false
		}
	}

	printHint("MCWOD can set the whole server up for you, so you never have to",
		"open a terminal on that PC yourself.")
	if !p.yesNo("Set up a Minecraft server on that PC now?", len(facts.Containers) == 0) {
		return false
	}

	if !acceptEULAOnce(p, cfg) {
		return false
	}

	dir := p.line("Where should the server live on that PC?", defaultComposeDir(s, cfg))
	target := inspectComposeTarget(s, dir)
	if target.Command == "" {
		printWarning("Docker Compose does not work on that PC.")
		printHint("Install the compose plugin there, then try again.")
		return false
	}

	spec := askComposeSpec(p, cfg, facts)
	password, err := prepareRCONPassword(s, target)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return false
	}

	content, err := buildComposeContent(target, spec)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return false
	}

	if !writeComposeFiles(p, s, target, spec, content, password) {
		return false
	}

	cfg.Server.ContainerName = spec.ServiceName
	cfg.Server.MCPort = spec.MCPort
	cfg.Server.ComposeDir = target.Dir
	rememberFirstWorld(cfg, spec, target.Dir)
	return true
}

// Without this the list stays empty and 'worlds' says the server was set up
// before MCWOD kept track, about a world MCWOD just created itself.
func rememberFirstWorld(cfg *Config, spec ComposeSpec, dir string) {
	cfg.Worlds.List = append(cfg.Worlds.List, World{
		Name:      spec.ServiceName,
		Container: spec.ServiceName,
		Port:      spec.MCPort,
		Version:   spec.MCVersion,
		Type:      spec.ServerType,
		Dir:       dir,
	})
	if cfg.Worlds.Active == "" {
		cfg.Worlds.Active = spec.ServiceName
	}
}

// Default yes, but never silently: the text says what saying yes means.
func acceptEULA(p *prompter) bool {
	fmt.Println("\nMinecraft needs you to accept Mojang's rules, the EULA.")
	printHint(eulaURL, "Saying yes here accepts them in your name.")
	return p.yesNo("Do you accept them?", true)
}

func defaultComposeDir(s *ServerSession, cfg *Config) string {
	if s.Platform().Windows {
		return `C:\Users\` + cfg.Server.SSHUser + `\minecraft`
	}
	return "/home/" + cfg.Server.SSHUser + "/minecraft"
}

func askComposeSpec(p *prompter, cfg *Config, facts ServerFacts) ComposeSpec {
	spec := defaultComposeSpec(cfg.Server.ContainerName, cfg.Server.MCPort)

	spec.ServiceName = p.validated("What should this world be called?", spec.ServiceName, validateContainerName)
	spec.BackupName = spec.ServiceName + "-backup"
	spec.ServerType = askServerType(p, spec.ServerType)
	spec.MCVersion = askMCVersion(p, spec.MCVersion)
	spec.Memory = askMemory(p, facts.MemoryGB, spec.Memory)
	spec.Admin = askAdmin(p)
	spec.Whitelist = askWhitelist(p, spec.Admin)

	spec.Backups = p.yesNo("Make a backup of your world automatically?", true)
	if spec.Backups {
		spec.BackupInterval = p.line("How often?", spec.BackupInterval)
		spec.KeepBackupDays = p.validatedInt("How many days of backups should be kept?", spec.KeepBackupDays, 1)
	}
	return spec
}

// Numbered, because a wrong word means retyping and these people are reading a
// terminal for the first time.
func askServerType(p *prompter, fallback string) string {
	fmt.Println("\nWhich kind of server?")
	for i, choice := range serverTypeChoices {
		fmt.Printf("  %d) %-9s %s\n", i+1, choice.name, hint(choice.what))
	}

	answer := p.validated("Pick one", "1", func(v string) error {
		if _, ok := serverTypeByChoice(v); ok {
			return nil
		}
		return fmt.Errorf("pick a number from 1 to %d", len(serverTypeChoices))
	})
	picked, _ := serverTypeByChoice(answer)
	return picked
}

// Takes the number or the name, so somebody who knows what PAPER is can type it.
func serverTypeByChoice(answer string) (string, bool) {
	answer = strings.ToUpper(strings.TrimSpace(answer))
	if index, err := strconv.Atoi(answer); err == nil {
		if index >= 1 && index <= len(serverTypeChoices) {
			return serverTypeChoices[index-1].name, true
		}
		return "", false
	}
	if contains(serverTypes, answer) {
		return answer, true
	}
	return "", false
}

// Empty means anyone with the address can join, which is the default a fresh
// server has. The first name given also becomes the operator.
// Asked in two steps, so somebody who does not want one answers once and moves
// on instead of facing an empty list they have to understand.
func askWhitelist(p *prompter, admin string) []string {
	fmt.Println("")
	if !p.yesNo("Do you want a whitelist, so only people you name can join?", false) {
		printHint("Anyone who knows your address can join.")
		return nil
	}

	answer := p.line("Which names may join? Separate them with commas", admin)
	names := []string{}
	for _, name := range splitList(answer) {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	if admin != "" && !contains(names, admin) {
		names = append(names, admin)
		printHint(admin + " was added too, an admin who cannot join is no use.")
	}
	return names
}

// A concrete version by default. LATEST moves the server under the world
// whenever the image is pulled again.
func askMCVersion(p *prompter, fallback string) string {
	answer := strings.TrimSpace(p.line("\nWhich Minecraft version?", fallback))
	if strings.EqualFold(answer, "LATEST") {
		printHint("Your server can then update itself to a new version on its own,",
			"and a world cannot go back to an older one afterwards.")
	}
	return answer
}

// A quarter of the machine, which is the number people are told to use anyway.
func askMemory(p *prompter, totalGB int, fallback string) string {
	if totalGB > 0 {
		fmt.Printf("\nThat PC has %d GB of RAM.\n", totalGB)
		fallback = suggestedMemory(totalGB)
	}
	return strings.TrimSpace(p.line("How much should Minecraft get?", fallback))
}

// Without one nobody can run a command in the game, not even the owner.
func askAdmin(p *prompter) string {
	name := strings.TrimSpace(p.line("\nWho should be the admin? Enter their Minecraft name", ""))
	if name != "" {
		printHint(name + " is now a Minecraft admin.")
	}
	return name
}

// An existing password is left alone, replacing it would break whatever else
// already reads it.
func prepareRCONPassword(s *ServerSession, target ComposeTarget) (string, error) {
	if hasRCONPasswordVar(target.ExistingEnv) {
		fmt.Printf("\n%s is already set in %s, keeping it.\n", rconPasswordVar, target.EnvFile)
		return "", nil
	}
	password, err := generateRCONPassword()
	if err != nil {
		return "", fmt.Errorf("cannot generate an RCON password: %w", err)
	}
	return password, nil
}

func buildComposeContent(target ComposeTarget, spec ComposeSpec) (string, error) {
	if target.Exists() {
		fmt.Printf("\n%s already exists, the two services are added to it.\n", target.File)
		return addServicesToCompose(target.Existing, spec)
	}
	return newComposeFile(spec)
}

func writeComposeFiles(p *prompter, s *ServerSession, target ComposeTarget,
	spec ComposeSpec, content, password string) bool {

	if target.Exists() && !p.yesNo("Write the changed "+target.File, true) {
		fmt.Println("Nothing was written.")
		return false
	}

	backup, err := backupComposeFile(s, target)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		fmt.Println("Nothing was written, the existing file could not be copied first.")
		return false
	}
	if backup != "" {
		fmt.Printf("The file that was there is now %s\n", backup)
	}

	// Before either file, the directory may not exist yet.
	if out, err := s.Run(makeDirCommand(s, target.Dir)); err != nil {
		fmt.Printf("\nCannot create %s: %v: %s\n", target.Dir, err, sanitizeForLog(out, 200))
		return false
	}

	if password != "" {
		env := appendRCONPassword(target.ExistingEnv, password)
		if err := writeRemoteEnvFile(s, target.EnvFile, env); err != nil {
			fmt.Printf("\n%v\n", err)
			return false
		}
		if target.ExistingEnv == "" {
			fmt.Printf("Created %s with the RCON password\n", target.EnvFile)
		} else {
			fmt.Printf("RCON password appended to %s\n", target.EnvFile)
		}
	}

	if err := writeRemoteFile(s, target.File, content); err != nil {
		fmt.Printf("\n%v\n", err)
		return false
	}

	if err := validateComposeFile(s, target); err != nil {
		fmt.Printf("\ncompose rejected the result: %v\n", err)
		if backup != "" {
			fmt.Printf("Put the old one back with: mcwod restore-compose\n")
		}
		return false
	}
	fmt.Printf("Written to %s, compose accepts it.\n", target.File)

	if !p.yesNo("Start the containers now?", true) {
		fmt.Printf("Start them yourself with: cd %s && %s up -d\n", target.Dir, target.Command)
		return true
	}
	out, err := composeUp(s, target)
	if err != nil {
		fmt.Printf("  could not start them: %v\n", err)
		fmt.Printf("  %s\n", sanitizeForLog(out, 400))
		return true
	}
	fmt.Println("Containers started.")
	if spec.Backups {
		fmt.Printf("Backups run every %s into %s/backups.\n", spec.BackupInterval, target.Dir)
	}
	return true
}

func makeDirCommand(s *ServerSession, dir string) string {
	if s.Platform().Windows {
		return "New-Item -ItemType Directory -Force -Path " + powerShellQuote(dir) + " | Out-Null"
	}
	return "mkdir -p " + shellQuote(dir)
}

// Puts a compose file the watcher replaced back, and keeps the current one so
// the restore itself can be undone.
func runRestoreCompose() int {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		return 1
	}

	p := newPrompter()
	fmt.Printf("Logging in as %s@%s to look for backups.\n", cfg.Server.SSHUser, cfg.Server.IP)
	fmt.Println("The password is used for this one login and is not stored anywhere.")
	password := p.secret(fmt.Sprintf("Password for %s@%s", cfg.Server.SSHUser, cfg.Server.IP))
	if password == "" {
		fmt.Println("\nNo password given, nothing was changed.")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	session, err := DialServerSession(ctx, NewSSHRunner(cfg), password, p)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}
	defer session.Close()
	session.Detect()

	dir := cfg.Server.ComposeDir
	if dir == "" {
		dir = p.line("Where does your server live on that PC?", defaultComposeDir(session, cfg))
	}
	backups, _ := listComposeBackups(session, dir)
	if len(backups) == 0 {
		fmt.Printf("\nNo backups from mcwod in %s.\n", dir)
		return 1
	}

	fmt.Println("\nBackups, newest first:")
	for i, name := range backups {
		fmt.Printf("  %d) %s\n", i+1, name)
	}
	choice := p.validated("Which one to restore", "1", func(v string) error {
		index, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || index < 1 || index > len(backups) {
			return fmt.Errorf("pick a number between 1 and %d", len(backups))
		}
		return nil
	})
	index, _ := strconv.Atoi(strings.TrimSpace(choice))
	chosen := backups[index-1]

	target := inspectComposeTarget(session, dir)
	if _, err := backupComposeFile(session, target); err != nil {
		fmt.Printf("\nCannot keep the current file first: %v\n", err)
		return 1
	}

	body, err := readRemoteFile(session, joinRemote(session, dir, chosen))
	if err != nil || strings.TrimSpace(body) == "" {
		fmt.Printf("\nCannot read %s\n", chosen)
		return 1
	}
	if err := writeRemoteFile(session, target.File, body); err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}

	fmt.Printf("\nRestored %s to %s\n", chosen, target.File)
	fmt.Println("The version that was there was kept as another backup first.")
	fmt.Printf("Apply it with: cd %s && %s up -d\n", dir, target.Command)
	return 0
}
