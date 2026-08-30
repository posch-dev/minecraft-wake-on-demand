package config

func Default() Config {
	return Config{
		Watcher: WatcherConfig{ListenAddress: "0.0.0.0", ListenPort: 25565},
		Server: ServerConfig{
			MCPort:           25565,
			SSHStrictHostKey: "accept-new",
			ContainerName:    "minecraft",
		},
		WoL:      WoLConfig{Mode: "broadcast"},
		DuckDNS:  DuckDNSConfig{UpdateIntervalHours: 6},
		Transfer: TransferConfig{Port: 25566},
		Timeouts: TimeoutsConfig{BootTimeout: 60, MCReadyTimeout: 30},
		Update:   UpdateConfig{Check: true},
		Sleep: SleepConfig{
			Enabled: false, IdleAfter: 900, ConfirmDelay: 60,
			GracePeriod: 900, PollInterval: 300, Action: "suspend",
		},
		Limits: LimitsConfig{
			BootCooldown: 10, BootFailureBackoff: 60, BootMaxBackoff: 900,
			MaxLogins: 0, MaxPerIP: 8,
		},
		MOTD: MOTDConfig{
			Sleeping:   DefaultMOTDSleeping,
			Starting:   DefaultMOTDStarting,
			LoginWait:  DefaultMOTDLoginWait,
			ServerFull: DefaultMOTDServerFull,
			MaxPlayers: 10,
		},
	}
}

// Env var, then repo root, then next to the binary.
