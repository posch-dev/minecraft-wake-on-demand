package main

import (
	"context"
	"fmt"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
)

// With the helper, a bare verb. Without it, the whole command, which is what
// a plain key and the older forced command expect.
func RunVerb(ctx context.Context, r *sshx.SSHRunner, verb string) (string, error) {
	if r.Config().Server.RemoteHelper {
		return r.Run(ctx, verb)
	}
	command, err := directCommand(r.Config(), verb)
	if err != nil {
		return "", err
	}
	return r.Run(ctx, command)
}

// A restricted key may ignore the command entirely, which is the recommended
// setup, so an empty response is success as long as the exit status is zero.
func StartContainer(ctx context.Context, r *sshx.SSHRunner) error {
	logging.Infof("Starting container %s via SSH", r.Config().Server.ContainerName)

	out, err := RunVerb(ctx, r, remoteVerbStart)
	if err != nil {
		if out != "" {
			return fmt.Errorf("%w: %s", err, logging.Sanitize(out, 200))
		}
		return err
	}
	logging.Infof("Container started successfully")
	return nil
}

// Used before a hibernate or shutdown so the world is written out. Suspend does
// not need it, the process simply resumes.
func StopContainer(ctx context.Context, r *sshx.SSHRunner) error {
	logging.Infof("Stopping container %s via SSH", r.Config().Server.ContainerName)

	out, err := RunVerb(ctx, r, remoteVerbStop)
	if err != nil {
		if out != "" {
			return fmt.Errorf("%w: %s", err, logging.Sanitize(out, 200))
		}
		return err
	}
	return nil
}
