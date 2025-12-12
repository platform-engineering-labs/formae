// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/imconc"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	pidFile = "/tmp/formae.pid"
)

type Agent struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	cfg    *pkgmodel.Config
	id     string
}

func New(cfg *pkgmodel.Config, id string) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		cfg:    cfg,
		id:     id,
	}
}

func (a *Agent) Start() error {
	// Check if already running
	if _, err := os.Stat(pidFile); err == nil {
		return fmt.Errorf("agent appears to be already running (PID file exists)")
	}

	logging.SetupBackendLogging(&a.cfg.Agent.Logging, &a.cfg.Agent.OTel)

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Write PID
	pid := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", pid)), 0600); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	imwg := imconc.NewConcGroup()
	go func() {
		// Important: Setup global tracer provider before db connections are created -
		// otelsql captures the tracer provider at driver registration time.
		shutdownTracer := api.SetupGlobalTracerProvider(&a.cfg.Agent.OTel)
		defer func() {
			shutdownTracer()
			a.cleanup()
			close(a.done)
		}()

		pluginManager := plugin.NewManager(util.ExpandHomePath(a.cfg.Plugins.PluginDir))
		pluginManager.Load()

		slog.Info("Starting agent", "id", a.id)

		ms, err := metastructure.NewMetastructure(a.ctx, a.cfg, pluginManager, a.id)
		if err != nil {
			slog.Error("Failed to create ms", "error", err)
			return
		}
		imwg.Add(ms)

		if err := ms.Start(); err != nil {
			slog.Error("Failed to start metastructure", "error", err)
			return
		}

		slog.Info("Agent started")

		apiServer := api.NewServer(a.ctx, ms, pluginManager, &a.cfg.Agent.Server, &a.cfg.Plugins, &a.cfg.Agent.OTel)
		imwg.Add(apiServer)
		imwg.Go(func() {
			apiServer.Start()
		})

		// Handle signals and shutdown
		go func() {
			select {
			case sig := <-sigChan:
				slog.Info("Received signal", "signal", sig)
				a.cancel()
			case <-a.ctx.Done():
				slog.Info("Context canceled")
			}
		}()
		<-a.ctx.Done()

		// Ensure app doesn't hang indefinitely on shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		slog.Info("Stopping metastructure")
		ms.Stop(false)

		done := make(chan struct{})
		go func() {
			imwg.Wait()
			close(done)
		}()

		// Wait for either completion or timeout
		select {
		case <-done:
			slog.Info("Components stopped gracefully")
		case <-shutdownCtx.Done():
			slog.Warn("Shutdown timed out, forcing stop")
			imwg.Stop(true)
		}
	}()

	return nil
}

func (a *Agent) Stop() error {
	// Try to read PID file
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("agent is not running (no PID file found)")
		}
		return fmt.Errorf("failed to read pid file: %w", err)
	}

	// Parse PID
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		return fmt.Errorf("invalid pid file content: %w", err)
	}

	// Check if we are the process
	if pid == os.Getpid() {
		if a.cancel != nil {
			a.cancel()
		}
		<-a.done
		return nil
	}

	// We are not the process, try to stop it
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// First, try SIGTERM
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if err.Error() == "os: process already finished" {
			a.cleanup()
			return fmt.Errorf("agent is not running (stale PID file)")
		}
		return fmt.Errorf("failed to send signal to process: %w", err)
	}

	if waitForPidFileRemoval(10 * time.Second) {
		return nil
	}

	// If SIGTERM didn't work, try SIGKILL as a last resort
	slog.Warn("SIGTERM timeout, attempting SIGKILL")
	if err := process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to SIGKILL process: %w", err)
	}

	// Clean up the PID file since SIGKILL won't trigger normal cleanup
	a.cleanup()
	return nil
}

func (a *Agent) Wait() {
	<-a.done
}

func (a *Agent) cleanup() {
	slog.Info("Cleaning up")
	// Remove PID file
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		slog.Error("Failed to remove pid file", "error", err)
	}
}

func waitForPidFileRemoval(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); os.IsNotExist(err) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
