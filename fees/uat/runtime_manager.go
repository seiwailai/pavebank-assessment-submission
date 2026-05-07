package uat

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

type managedRuntime struct {
	namespace        string
	appPort          int
	temporalPort     int
	temporalImage    string
	postgresImage    string
	networkName      string
	temporalName     string
	postgresName     string
	temporalHostPort string
	startupTimeout   time.Duration
	encoreCmd        *exec.Cmd
	encoreLogPath    string
}

func startManagedRuntime() (*managedRuntime, error) {
	namespace := strings.TrimSpace(*uatNamespaceFlag)
	if namespace == "" {
		namespace = "uat-" + uuid.NewString()
	}

	mgr := &managedRuntime{
		namespace:        namespace,
		appPort:          *uatAppPortFlag,
		temporalPort:     *uatTemporalPortFlag,
		temporalImage:    *uatTemporalImageFlag,
		postgresImage:    "postgres:15-alpine",
		networkName:      "fees-uat-net-" + strings.ReplaceAll(namespace, "_", "-"),
		temporalName:     "fees-uat-temporal-" + strings.ReplaceAll(namespace, "_", "-"),
		postgresName:     "fees-uat-postgres-" + strings.ReplaceAll(namespace, "_", "-"),
		temporalHostPort: fmt.Sprintf("127.0.0.1:%d", *uatTemporalPortFlag),
		startupTimeout:   *uatStartupTimeoutFlag,
	}

	mgr.logf("starting managed runtime setup")
	if err := mgr.createNamespace(); err != nil {
		return nil, err
	}
	if err := mgr.createDockerNetwork(); err != nil {
		_ = mgr.deleteNamespace()
		return nil, err
	}
	if err := mgr.startPostgres(); err != nil {
		_ = mgr.deleteDockerNetwork()
		_ = mgr.deleteNamespace()
		return nil, err
	}
	if err := mgr.waitForPostgresReady(); err != nil {
		_ = mgr.stopPostgres()
		_ = mgr.deleteDockerNetwork()
		_ = mgr.deleteNamespace()
		return nil, err
	}
	if err := mgr.startTemporal(); err != nil {
		_ = mgr.stopPostgres()
		_ = mgr.deleteDockerNetwork()
		_ = mgr.deleteNamespace()
		return nil, err
	}
	if err := mgr.waitForTemporalReady(); err != nil {
		_ = mgr.stopTemporal()
		_ = mgr.stopPostgres()
		_ = mgr.deleteDockerNetwork()
		_ = mgr.deleteNamespace()
		return nil, err
	}
	if err := mgr.startEncore(); err != nil {
		_ = mgr.stopTemporal()
		_ = mgr.stopPostgres()
		_ = mgr.deleteDockerNetwork()
		_ = mgr.deleteNamespace()
		return nil, err
	}
	if err := mgr.waitForHealth(); err != nil {
		_ = mgr.Stop()
		return nil, err
	}

	mgr.logf("managed runtime setup complete")
	return mgr, nil
}

func (m *managedRuntime) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.appPort)
}

func (m *managedRuntime) Stop() error {
	m.logf("stopping managed runtime")
	var errs []string
	if err := m.stopEncore(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := m.stopTemporal(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := m.stopPostgres(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := m.deleteDockerNetwork(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := m.deleteNamespace(); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	m.logf("managed runtime stopped")
	return nil
}

func (m *managedRuntime) createNamespace() error {
	m.logf("creating Encore namespace %q", m.namespace)
	cmd := exec.Command("encore", "namespace", "create", m.namespace)
	cmd.Dir = repoRoot()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "already exists") {
			m.logf("Encore namespace %q already exists; reusing it", m.namespace)
			return nil
		}
		return fmt.Errorf("create namespace %q: %w: %s", m.namespace, err, stderr.String())
	}
	m.logf("created Encore namespace %q", m.namespace)
	return nil
}

func (m *managedRuntime) deleteNamespace() error {
	m.logf("deleting Encore namespace %q", m.namespace)
	cmd := exec.Command("encore", "namespace", "delete", m.namespace)
	cmd.Dir = repoRoot()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "not found") {
			m.logf("Encore namespace %q already removed", m.namespace)
			return nil
		}
		return fmt.Errorf("delete namespace %q: %w: %s", m.namespace, err, stderr.String())
	}
	m.logf("deleted Encore namespace %q", m.namespace)
	return nil
}

func (m *managedRuntime) createDockerNetwork() error {
	m.logf("creating Docker network %q", m.networkName)
	cmd := exec.Command("docker", "network", "create", m.networkName)
	cmd.Dir = repoRoot()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "already exists") {
			m.logf("Docker network %q already exists; reusing it", m.networkName)
			return nil
		}
		return fmt.Errorf("create Docker network %q: %w: %s", m.networkName, err, stderr.String())
	}
	m.logf("created Docker network %q", m.networkName)
	return nil
}

func (m *managedRuntime) deleteDockerNetwork() error {
	m.logf("deleting Docker network %q", m.networkName)
	cmd := exec.Command("docker", "network", "rm", m.networkName)
	cmd.Dir = repoRoot()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		lower := strings.ToLower(stderr.String())
		if strings.Contains(lower, "not found") || strings.Contains(lower, "no such network") {
			m.logf("Docker network %q already removed", m.networkName)
			return nil
		}
		return fmt.Errorf("delete Docker network %q: %w: %s", m.networkName, err, stderr.String())
	}
	m.logf("deleted Docker network %q", m.networkName)
	return nil
}

func (m *managedRuntime) startPostgres() error {
	m.logf("starting PostgreSQL container %q using image %q", m.postgresName, m.postgresImage)
	_ = exec.Command("docker", "rm", "-f", m.postgresName).Run()

	cmd := exec.Command(
		"docker", "run", "-d",
		"--name", m.postgresName,
		"--network", m.networkName,
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_USER=postgres",
		m.postgresImage,
	)
	cmd.Dir = repoRoot()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start PostgreSQL container %q: %w: %s", m.postgresName, err, stderr.String())
	}
	containerID := strings.TrimSpace(stdout.String())
	if containerID != "" {
		m.logf("started PostgreSQL container %q (id=%s)", m.postgresName, containerID)
	} else {
		m.logf("started PostgreSQL container %q", m.postgresName)
	}
	return nil
}

func (m *managedRuntime) stopPostgres() error {
	m.logf("stopping PostgreSQL container %q", m.postgresName)
	cmd := exec.Command("docker", "rm", "-f", m.postgresName)
	cmd.Dir = repoRoot()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "no such container") {
			m.logf("PostgreSQL container %q already removed", m.postgresName)
			return nil
		}
		return fmt.Errorf("remove PostgreSQL container %q: %w: %s", m.postgresName, err, stderr.String())
	}
	m.logf("stopped PostgreSQL container %q", m.postgresName)
	return nil
}

func (m *managedRuntime) waitForPostgresReady() error {
	m.logf("waiting for PostgreSQL readiness in container %q", m.postgresName)
	deadline := time.Now().Add(m.startupTimeout)
	var lastErr error
	nextProgressLog := time.Now()
	for time.Now().Before(deadline) {
		state, inspectErr := m.postgresContainerState()
		if inspectErr != nil {
			//nolint:ineffassign,staticcheck
			lastErr = inspectErr
		} else if state != "running" {
			logTail := m.postgresLogsTail()
			if logTail != "" {
				return fmt.Errorf("postgres container %q is not running (state=%s). recent logs:\n%s", m.postgresName, state, logTail)
			}
			return fmt.Errorf("postgres container %q is not running (state=%s)", m.postgresName, state)
		}

		cmd := exec.Command("docker", "exec", m.postgresName, "pg_isready", "-U", "postgres")
		cmd.Dir = repoRoot()
		if output, err := cmd.CombinedOutput(); err == nil {
			m.logf("PostgreSQL is ready")
			return nil
		} else {
			lastErr = fmt.Errorf("pg_isready failed: %s", strings.TrimSpace(string(output)))
		}

		if time.Now().After(nextProgressLog) {
			m.logf("still waiting for PostgreSQL readiness: %v", lastErr)
			nextProgressLog = time.Now().Add(5 * time.Second)
		}
		time.Sleep(time.Second)
	}

	logTail := m.postgresLogsTail()
	if logTail != "" {
		return fmt.Errorf("wait for PostgreSQL readiness: %w. recent logs:\n%s", lastErr, logTail)
	}
	return fmt.Errorf("wait for PostgreSQL readiness: %w", lastErr)
}

func (m *managedRuntime) startTemporal() error {
	m.logf("starting Temporal container %q using image %q on host port %d", m.temporalName, m.temporalImage, m.temporalPort)
	_ = exec.Command("docker", "rm", "-f", m.temporalName).Run()

	cmd := exec.Command(
		"docker", "run", "-d",
		"--name", m.temporalName,
		"--network", m.networkName,
		"-p", fmt.Sprintf("%d:7233", m.temporalPort),
		"-e", "DB=postgres12",
		"-e", "DB_PORT=5432",
		"-e", "POSTGRES_SEEDS="+m.postgresName,
		"-e", "POSTGRES_USER=postgres",
		"-e", "POSTGRES_PWD=postgres",
		m.temporalImage,
	)
	cmd.Dir = repoRoot()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start temporal container %q: %w: %s", m.temporalName, err, stderr.String())
	}
	containerID := strings.TrimSpace(stdout.String())
	if containerID != "" {
		m.logf("started Temporal container %q (id=%s)", m.temporalName, containerID)
	} else {
		m.logf("started Temporal container %q", m.temporalName)
	}
	return nil
}

func (m *managedRuntime) stopTemporal() error {
	m.logf("stopping Temporal container %q", m.temporalName)
	cmd := exec.Command("docker", "rm", "-f", m.temporalName)
	cmd.Dir = repoRoot()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "no such container") {
			m.logf("Temporal container %q already removed", m.temporalName)
			return nil
		}
		return fmt.Errorf("remove temporal container %q: %w: %s", m.temporalName, err, stderr.String())
	}
	m.logf("stopped Temporal container %q", m.temporalName)
	return nil
}

func (m *managedRuntime) startEncore() error {
	m.logf("starting Encore app on port %d in namespace %q", m.appPort, m.namespace)
	logFile, err := os.CreateTemp("", "fees-uat-encore-*.log")
	if err != nil {
		return fmt.Errorf("create encore log file: %w", err)
	}
	m.encoreLogPath = logFile.Name()

	cmd := exec.Command(
		"encore", "run",
		"--watch=false",
		"--browser=never",
		"--namespace", m.namespace,
		"--port", fmt.Sprintf("%d", m.appPort),
	)
	cmd.Dir = repoRoot()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"FEES_TEMPORAL_HOSTPORT="+m.temporalHostPort,
		"FEES_TEMPORAL_NAMESPACE=default",
	)

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start encore runtime: %w", err)
	}
	_ = logFile.Close()

	m.encoreCmd = cmd
	m.logf("Encore process started; logs streaming to %s", m.encoreLogPath)
	return nil
}

func (m *managedRuntime) stopEncore() error {
	if m.encoreCmd == nil || m.encoreCmd.Process == nil {
		m.logf("Encore process was not started or is already stopped")
		return nil
	}

	m.logf("stopping Encore app process")
	_ = m.encoreCmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() {
		done <- m.encoreCmd.Wait()
	}()

	select {
	case <-time.After(15 * time.Second):
		m.logf("Encore app did not stop within 15s; killing process")
		_ = m.encoreCmd.Process.Kill()
		<-done
	case <-done:
	}
	m.logf("Encore app process stopped")
	return nil
}

func (m *managedRuntime) waitForTemporalReady() error {
	m.logf("waiting for Temporal readiness at %s", m.temporalHostPort)
	deadline := time.Now().Add(m.startupTimeout)
	var lastErr error
	var lastHealthOutput string
	nextProgressLog := time.Now()
	for time.Now().Before(deadline) {
		state, inspectErr := m.temporalContainerState()
		if inspectErr != nil {
			//nolint:ineffassign,staticcheck
			lastErr = inspectErr
		} else if state != "running" {
			logTail := m.temporalLogsTail()
			if logTail != "" {
				return fmt.Errorf("temporal container %q is not running (state=%s). recent logs:\n%s", m.temporalName, state, logTail)
			}
			return fmt.Errorf("temporal container %q is not running (state=%s)", m.temporalName, state)
		}

		healthCmd := exec.Command(
			"docker", "exec", m.temporalName, "sh", "-c",
			"temporal operator cluster health 2>&1 || tctl cluster health 2>&1",
		)
		if output, err := healthCmd.CombinedOutput(); err == nil {
			m.logf("Temporal cluster is ready")
			return nil
		} else if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
			lastHealthOutput = trimmed
			//nolint:ineffassign,staticcheck
			lastErr = fmt.Errorf("cluster health probe failed: %s", trimmed)
		}

		if logs := m.temporalLogsTail(); temporalBootstrapLooksReady(logs) {
			m.logf("Temporal bootstrap markers detected in logs; treating runtime as ready")
			return nil
		}

		conn, err := net.DialTimeout("tcp", m.temporalHostPort, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			if lastHealthOutput != "" {
				lastErr = fmt.Errorf("temporal tcp port is open but cluster health is not ready yet: %s", lastHealthOutput)
			} else {
				lastErr = fmt.Errorf("temporal tcp port is open but cluster health is not ready yet")
			}
		} else {
			lastErr = err
		}
		if time.Now().After(nextProgressLog) {
			m.logf("still waiting for Temporal readiness: %v", lastErr)
			if logs := m.temporalLogsTail(); logs != "" {
				m.logf("recent Temporal logs:\n%s", logs)
			}
			nextProgressLog = time.Now().Add(5 * time.Second)
		}
		time.Sleep(time.Second)
	}

	logTail := m.temporalLogsTail()
	if logTail != "" {
		return fmt.Errorf("wait for temporal cluster readiness at %s: %w. recent logs:\n%s", m.temporalHostPort, lastErr, logTail)
	}
	return fmt.Errorf("wait for temporal cluster readiness at %s: %w", m.temporalHostPort, lastErr)
}

func (m *managedRuntime) waitForHealth() error {
	m.logf("waiting for Fees health endpoint at %s/v1/health/fees", m.BaseURL())
	deadline := time.Now().Add(m.startupTimeout)
	client := &http.Client{Timeout: 3 * time.Second}
	var lastErr error
	nextProgressLog := time.Now()

	for time.Now().Before(deadline) {
		resp, err := client.Get(m.BaseURL() + "/v1/health/fees")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				m.logf("Fees health endpoint is ready")
				return nil
			}
			lastErr = fmt.Errorf("health returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(nextProgressLog) {
			m.logf("still waiting for Fees health endpoint: %v", lastErr)
			nextProgressLog = time.Now().Add(5 * time.Second)
		}
		time.Sleep(time.Second)
	}

	logHint := ""
	if m.encoreLogPath != "" {
		logHint = " encore log: " + m.encoreLogPath
	}
	return fmt.Errorf("wait for encore health endpoint: %w.%s", lastErr, logHint)
}

func repoRoot() string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func (m *managedRuntime) logf(format string, args ...any) {
	prefix := "UAT runtime"
	if m != nil && m.namespace != "" {
		prefix = fmt.Sprintf("UAT runtime [%s]", m.namespace)
	}
	fmt.Fprintf(os.Stderr, "%s %s: %s\n", time.Now().Format(time.RFC3339), prefix, fmt.Sprintf(format, args...))
}

func (m *managedRuntime) temporalContainerState() (string, error) {
	cmd := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", m.temporalName)
	cmd.Dir = repoRoot()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect temporal container %q: %w (%s)", m.temporalName, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *managedRuntime) temporalLogsTail() string {
	cmd := exec.Command("docker", "logs", "--tail", "50", m.temporalName)
	cmd.Dir = repoRoot()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(output))
	}
	return strings.TrimSpace(string(output))
}

func temporalBootstrapLooksReady(logs string) bool {
	if logs == "" {
		return false
	}

	markers := []string{
		"Namespace default successfully registered.",
		"Default namespace default registration complete.",
		"Register namespace succeeded",
	}
	for _, marker := range markers {
		if strings.Contains(logs, marker) {
			return true
		}
	}
	return false
}

func (m *managedRuntime) postgresContainerState() (string, error) {
	cmd := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", m.postgresName)
	cmd.Dir = repoRoot()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect postgres container %q: %w (%s)", m.postgresName, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *managedRuntime) postgresLogsTail() string {
	cmd := exec.Command("docker", "logs", "--tail", "50", m.postgresName)
	cmd.Dir = repoRoot()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(output))
	}
	return strings.TrimSpace(string(output))
}
