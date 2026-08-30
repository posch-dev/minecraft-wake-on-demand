package main

import "fmt"

const eulaURL = "https://aka.ms/MinecraftEULA"

// Sets the Minecraft and backup containers up on the server, either as a fresh
// compose file or as two services added to one that is already there.
func offerContainerSetup(p *prompter, s *ServerSession, cfg *Config, facts ServerFacts) bool {
	fmt.Println("\n--- Minecraft container ---")
	if !facts.Platform.HasDocker {
		fmt.Println("No docker on the server, so there is nothing to set up yet.")
		fmt.Println("Install Docker there, then run 'mc-wol-proxy config' and pick this again.")
		return false
	}
	if len(facts.Containers) > 0 {
		fmt.Printf("The server already runs a container called %q.\n", cfg.Server.ContainerName)
		if !p.yesNo("Set up another one anyway", false) {
			return false
		}
	}

	fmt.Println("The watcher can write the compose file and start the containers for you,")
	fmt.Println("so you never have to log in to the server PC yourself.")
	if !p.yesNo("Set up the Minecraft container now", len(facts.Containers) == 0) {
		return false
	}

	if !acceptEULA(p) {
		fmt.Println("Without accepting it the server cannot run, so nothing was written.")
		return false
	}

	dir := p.line("Directory on the server for the compose file", defaultComposeDir(s, cfg))
	target := inspectComposeTarget(s, dir)
	if target.Command == "" {
		fmt.Println("\nNeither 'docker compose' nor 'docker-compose' works on the server.")
		fmt.Println("Install the compose plugin there, then try again.")
		return false
	}

	spec := askComposeSpec(p, cfg, target)
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
	return true
}

// Default yes, but never silently: the text says what saying yes means.
func acceptEULA(p *prompter) bool {
	fmt.Println("\nThe Minecraft server needs you to accept Mojang's End User License")
	fmt.Println("Agreement. Saying yes here writes EULA=TRUE into the compose file on")
	fmt.Println("your behalf, which is the same as accepting it yourself.")
	fmt.Println("  " + eulaURL)
	return p.yesNo("Do you accept the Minecraft EULA", true)
}

func defaultComposeDir(s *ServerSession, cfg *Config) string {
	if s.Platform().Windows {
		return `C:\Users\` + cfg.Server.SSHUser + `\minecraft`
	}
	return "/home/" + cfg.Server.SSHUser + "/minecraft"
}

func askComposeSpec(p *prompter, cfg *Config, target ComposeTarget) ComposeSpec {
	spec := defaultComposeSpec(cfg.Server.ContainerName, cfg.Server.MCPort)

	spec.ServiceName = p.validated("Name for the container", spec.ServiceName, validateContainerName)
	spec.BackupName = spec.ServiceName + "-backup"
	spec.MCVersion = p.line("Minecraft version, LATEST for the newest", spec.MCVersion)
	spec.Memory = p.line("Memory for the server, for example 4G", spec.Memory)
	spec.MCPort = p.validatedPort("Port to publish on the server PC", spec.MCPort)
	spec.DataDir = p.line("Where the world lives, relative to the compose file", spec.DataDir)

	spec.Backups = p.yesNo("Add the automatic backup container as well", true)
	if spec.Backups {
		spec.BackupInterval = p.line("How often to back up", spec.BackupInterval)
		spec.KeepBackupDays = p.validatedInt("Days to keep old backups", spec.KeepBackupDays, 1)
	}
	return spec
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

	if password != "" {
		env := appendRCONPassword(target.ExistingEnv, password)
		if err := writeRemoteEnvFile(s, target.EnvFile, env); err != nil {
			fmt.Printf("\n%v\n", err)
			return false
		}
		fmt.Printf("RCON password written to %s\n", target.EnvFile)
	}

	if out, err := s.Run(makeDirCommand(s, target.Dir)); err != nil {
		fmt.Printf("\nCannot create %s: %v: %s\n", target.Dir, err, sanitizeForLog(out, 200))
		return false
	}
	if err := writeRemoteFile(s, target.File, content); err != nil {
		fmt.Printf("\n%v\n", err)
		return false
	}

	if err := validateComposeFile(s, target); err != nil {
		fmt.Printf("\ncompose rejected the result: %v\n", err)
		if backup != "" {
			fmt.Printf("Put the old one back with: mc-wol-proxy restore-compose\n")
		}
		return false
	}
	fmt.Printf("Written to %s, compose accepts it.\n", target.File)

	if !p.yesNo("Start the containers now", true) {
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
