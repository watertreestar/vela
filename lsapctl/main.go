package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/briandowns/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

type RuntimeManifest struct {
	SchemaVersion string            `json:"schemaVersion"`
	BuildID       string            `json:"buildId"`
	ProductID     string            `json:"productId"`
	Version       string            `json:"version"`
	InstallDir    string            `json:"installDir"`
	Files         RuntimeFiles      `json:"files"`
	Workloads     []RuntimeWorkload `json:"workloads"`
}

type RuntimeFiles struct {
	Compose    string `json:"compose"`
	SystemdDir string `json:"systemdDir"`
	Config     string `json:"config"`
	BuildLock  string `json:"buildLock"`
	Workloads  string `json:"workloads"`
}

type RuntimeWorkload struct {
	ID             string `json:"id"`
	Runtime        string `json:"runtime"`
	Component      string `json:"component"`
	Module         string `json:"module"`
	ComposeService string `json:"composeService"`
	UnitName       string `json:"unitName"`
}

type Options struct {
	Manifest    string
	Workload    string
	JSON        bool
	Yes         bool
	NoElevate   bool
	DryRun      bool
	Verbose     bool
	InstallDir  string
	BackupDir   string
	NoBackup    bool
	Interactive bool
}

type Check struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type DoctorResult struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

var opts Options
var (
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	styleInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
)

func main() {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		exitError(err)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "lsapctl",
		Short: "Manage LSAP package workloads",
		Long:  "lsapctl installs, starts, stops, restarts, checks, and uninstalls Docker and systemd workloads from lsapctl-runtime.json.",
		Example: strings.TrimSpace(`
./lsapctl doctor
./lsapctl install --interactive
./lsapctl restart --workload location-worker
./lsapctl status --json
./lsapctl install --yes
./lsapctl completion bash | sudo tee /etc/bash_completion.d/lsapctl >/dev/null
`),
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&opts.Manifest, "manifest", "", "runtime manifest path")
	root.PersistentFlags().StringVar(&opts.Workload, "workload", "", "operate on one workload id")
	root.PersistentFlags().BoolVar(&opts.JSON, "json", false, "print JSON output where supported")
	root.PersistentFlags().BoolVar(&opts.Yes, "yes", false, "assume yes for prompts")
	root.PersistentFlags().BoolVar(&opts.NoElevate, "no-elevate", false, "do not re-exec through sudo")
	root.PersistentFlags().BoolVar(&opts.DryRun, "dry-run", false, "print actions without executing commands")
	root.PersistentFlags().BoolVar(&opts.Verbose, "verbose", false, "print command details")
	root.PersistentFlags().StringVar(&opts.InstallDir, "install-dir", "", "override target install directory")
	root.PersistentFlags().StringVar(&opts.BackupDir, "backup-dir", "", "directory used for install backups")
	root.PersistentFlags().BoolVar(&opts.NoBackup, "no-backup", false, "skip backup before install")
	root.PersistentFlags().BoolVar(&opts.Interactive, "interactive", false, "prompt for install options")

	root.AddCommand(command("doctor", "Check OS, permissions, systemd, Docker, files, and conflicts", func(manifest RuntimeManifest, baseDir string) {
		result := runDoctor(manifest, baseDir, opts, false)
		printDoctor(result, opts.JSON)
		if !result.OK {
			os.Exit(2)
		}
	}))
	root.AddCommand(command("install", "Install systemd units and prepare Docker workloads", func(manifest RuntimeManifest, baseDir string) {
		manifest = resolveInstallOptions(manifest, opts)
		requireDoctor(manifest, baseDir, opts, true)
		runInstall(manifest, baseDir, opts)
	}))
	root.AddCommand(command("start", "Start Docker and systemd workloads", func(manifest RuntimeManifest, baseDir string) {
		requireDoctor(manifest, baseDir, opts, true)
		runStart(manifest, baseDir, opts)
	}))
	root.AddCommand(command("stop", "Stop Docker and systemd workloads", func(manifest RuntimeManifest, baseDir string) {
		requireDoctor(manifest, baseDir, opts, true)
		runStop(manifest, baseDir, opts)
	}))
	root.AddCommand(command("restart", "Restart Docker and systemd workloads", func(manifest RuntimeManifest, baseDir string) {
		requireDoctor(manifest, baseDir, opts, true)
		runStop(manifest, baseDir, opts)
		runStart(manifest, baseDir, opts)
	}))
	root.AddCommand(command("status", "Show workload status", func(manifest RuntimeManifest, baseDir string) {
		result := runDoctor(manifest, baseDir, opts, false)
		if opts.JSON {
			printDoctor(result, true)
		}
		runStatus(manifest, baseDir, opts)
	}))
	root.AddCommand(command("uninstall", "Stop and remove installed systemd units", func(manifest RuntimeManifest, baseDir string) {
		requireDoctor(manifest, baseDir, opts, true)
		runUninstall(manifest, baseDir, opts)
	}))

	return root
}

func command(name string, short string, run func(RuntimeManifest, string)) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, baseDir, err := loadManifest(opts.Manifest)
			if err != nil {
				return err
			}
			if opts.InstallDir != "" {
				manifest.InstallDir = opts.InstallDir
			}
			if shouldElevate(name, manifest, opts) {
				return elevate(name, opts)
			}
			run(manifest, baseDir)
			return nil
		},
	}
}

func loadManifest(path string) (RuntimeManifest, string, error) {
	candidates := []string{}
	if path != "" {
		candidates = append(candidates, path)
	} else {
		candidates = append(candidates,
			"rendered/lsapctl-runtime.json",
			"lsapctl-runtime.json",
			"/opt/lsap/rendered/lsapctl-runtime.json",
		)
	}
	var lastErr error
	for _, candidate := range candidates {
		content, err := os.ReadFile(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		var manifest RuntimeManifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return RuntimeManifest{}, "", err
		}
		if manifest.SchemaVersion != "lsapctl.runtime/v1" {
			return RuntimeManifest{}, "", fmt.Errorf("unsupported runtime manifest schema %q", manifest.SchemaVersion)
		}
		abs, _ := filepath.Abs(candidate)
		return manifest, filepath.Dir(filepath.Dir(abs)), nil
	}
	return RuntimeManifest{}, "", fmt.Errorf("runtime manifest not found: %w", lastErr)
}

func runDoctor(manifest RuntimeManifest, baseDir string, opts Options, strict bool) DoctorResult {
	checks := []Check{}
	add := func(id, severity, message string) {
		checks = append(checks, Check{ID: id, Severity: severity, Message: message})
	}

	if runtime.GOOS != "linux" {
		add("os.linux", "error", "lsapctl currently supports Linux runtime management")
	}
	if content, err := os.ReadFile("/etc/os-release"); err == nil {
		osRelease := string(content)
		if !strings.Contains(osRelease, "ID=centos") &&
			!strings.Contains(osRelease, "ID=\"centos\"") &&
			!strings.Contains(osRelease, "ID=rhel") &&
			!strings.Contains(osRelease, "ID=rocky") &&
			!strings.Contains(osRelease, "ID=almalinux") &&
			!strings.Contains(osRelease, "ID=ubuntu") &&
			!strings.Contains(osRelease, "ID=debian") {
			add("os.family", "warning", "Linux distribution is not explicitly certified")
		} else {
			add("os.family", "info", "Linux distribution detected")
		}
	} else {
		add("os.release", "warning", "/etc/os-release is not readable")
	}

	if strict && !isRoot() {
		add("permission.root", "error", "this command requires root privileges")
	}
	if manifest.InstallDir != "" {
		parent := filepath.Dir(manifest.InstallDir)
		if !pathExists(parent) {
			if strict && isRoot() {
				add("path.install_parent", "info", "install directory parent will be created: "+parent)
			} else {
				add("path.install_parent", "error", "install directory parent does not exist: "+parent)
			}
		}
	}

	if hasRuntime(manifest, "systemd") {
		if _, err := exec.LookPath("systemctl"); err != nil {
			add("systemd.systemctl", "error", "systemctl is required for systemd workloads")
		}
		if strict && !isWritable("/etc/systemd/system") {
			add("systemd.permission", "error", "/etc/systemd/system is not writable")
		}
		systemdDir := resolvePath(baseDir, manifest.Files.SystemdDir)
		if systemdDir == "" || !pathExists(systemdDir) {
			add("systemd.units", "error", "systemd unit directory is missing")
		}
	}

	if hasRuntime(manifest, "docker") {
		if _, err := exec.LookPath("docker"); err != nil {
			add("docker.cli", "error", "docker CLI is required for docker workloads")
		}
		if composeCommand() == nil {
			add("docker.compose", "error", "docker compose plugin or docker-compose is required")
		}
		if err := execCommand("docker", "info").Run(); err != nil {
			add("docker.daemon", "error", "Docker daemon is not reachable; if this is a permission error, run with sudo or add the user to the docker group")
		}
		compose := resolvePath(baseDir, manifest.Files.Compose)
		if compose == "" || !pathExists(compose) {
			add("docker.compose_file", "error", "docker compose file is missing")
		}
	}

	if _, err := exec.LookPath("getenforce"); err == nil {
		output, _ := exec.Command("getenforce").Output()
		if strings.TrimSpace(string(output)) == "Enforcing" {
			add("selinux.mode", "warning", "SELinux is enforcing; verify bind mount labels")
		}
	}
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		add("firewalld.present", "info", "firewalld is installed; verify required ports")
	}

	for _, workload := range filterWorkloads(manifest, opts.Workload) {
		if workload.Runtime == "systemd" && workload.UnitName != "" && pathExists(filepath.Join("/etc/systemd/system", workload.UnitName)) && strict {
			add("systemd.conflict."+workload.ID, "warning", "systemd unit already exists: "+workload.UnitName)
		}
	}

	ok := true
	for _, check := range checks {
		if check.Severity == "error" {
			ok = false
			break
		}
	}
	return DoctorResult{OK: ok, Checks: checks}
}

func requireDoctor(manifest RuntimeManifest, baseDir string, opts Options, strict bool) {
	result := runDoctor(manifest, baseDir, opts, strict)
	if opts.Verbose || !result.OK {
		printDoctor(result, opts.JSON)
	}
	if !result.OK {
		os.Exit(2)
	}
}

func printDoctor(result DoctorResult, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}
	if result.OK {
		fmt.Println(styleOK.Render("OK") + " environment checks passed")
	}
	rows := [][]string{}
	for _, check := range result.Checks {
		rows = append(rows, []string{severityLabel(check.Severity), check.ID, check.Message})
	}
	if len(rows) > 0 {
		fmt.Println(table.New().
			Border(lipgloss.NormalBorder()).
			Headers("Severity", "Check", "Message").
			Rows(rows...).
			Render())
	}
}

func runInstall(manifest RuntimeManifest, baseDir string, opts Options) {
	confirmInstallTarget(manifest, opts)
	if !opts.NoBackup {
		backupInstall(manifest, opts)
	}
	workloads := filterWorkloads(manifest, opts.Workload)
	progress := newProgress(len(workloads) + 2)
	if manifest.InstallDir != "" {
		progress.Step("sync package files to " + manifest.InstallDir)
		syncInstallDir(baseDir, manifest.InstallDir, opts)
		baseDir = manifest.InstallDir
	}
	for _, workload := range workloads {
		if workload.Runtime != "systemd" {
			continue
		}
		unitName := workload.UnitName
		source := filepath.Join(resolvePath(baseDir, manifest.Files.SystemdDir), unitName)
		target := filepath.Join("/etc/systemd/system", unitName)
		progress.Step("install systemd unit " + unitName)
		runCopy(source, target, opts)
	}
	progress.Step("reload systemd")
	runCmd(opts, "systemctl", "daemon-reload")
	for _, workload := range workloads {
		if workload.Runtime == "systemd" {
			runCmd(opts, "systemctl", "enable", workload.UnitName)
		}
	}
	fmt.Println(styleOK.Render("install completed"))
}

func syncInstallDir(source string, target string, opts Options) {
	sourceAbs, _ := filepath.Abs(source)
	targetAbs, _ := filepath.Abs(target)
	if sourceAbs == targetAbs {
		return
	}
	if strings.HasPrefix(targetAbs, sourceAbs+string(os.PathSeparator)) {
		exitError(fmt.Errorf("install directory cannot be inside package source directory: %s", targetAbs))
	}
	if opts.Verbose || opts.DryRun {
		fmt.Printf("+ sync %s %s\n", sourceAbs, targetAbs)
	}
	if opts.DryRun {
		return
	}
	if err := copyDir(sourceAbs, targetAbs); err != nil {
		exitError(err)
	}
}

func runStart(manifest RuntimeManifest, baseDir string, opts Options) {
	workloads := filterWorkloads(manifest, opts.Workload)
	progress := newProgress(len(workloads))
	if hasWorkloadRuntime(workloads, "docker") {
		progress.Step("start docker compose workloads")
		args := []string{"up", "-d"}
		if opts.Workload != "" {
			for _, workload := range workloads {
				if workload.Runtime == "docker" && workload.ComposeService != "" {
					args = append(args, workload.ComposeService)
				}
			}
		}
		runCompose(opts, resolvePath(baseDir, manifest.Files.Compose), args...)
	}
	for _, workload := range workloads {
		if workload.Runtime == "systemd" {
			progress.Step("start " + workload.UnitName)
			runCmd(opts, "systemctl", "start", workload.UnitName)
		}
	}
}

func runStop(manifest RuntimeManifest, baseDir string, opts Options) {
	workloads := filterWorkloads(manifest, opts.Workload)
	progress := newProgress(len(workloads))
	for _, workload := range workloads {
		if workload.Runtime == "systemd" {
			progress.Step("stop " + workload.UnitName)
			runCmd(opts, "systemctl", "stop", workload.UnitName)
		}
	}
	if hasWorkloadRuntime(workloads, "docker") {
		progress.Step("stop docker compose workloads")
		if opts.Workload != "" {
			for _, workload := range workloads {
				if workload.Runtime == "docker" && workload.ComposeService != "" {
					runCompose(opts, resolvePath(baseDir, manifest.Files.Compose), "stop", workload.ComposeService)
				}
			}
		} else {
			runCompose(opts, resolvePath(baseDir, manifest.Files.Compose), "down")
		}
	}
}

func runStatus(manifest RuntimeManifest, baseDir string, opts Options) {
	workloads := filterWorkloads(manifest, opts.Workload)
	if !opts.JSON {
		rows := [][]string{}
		for _, workload := range workloads {
			rows = append(rows, []string{workload.ID, workload.Runtime, workloadTarget(workload)})
		}
		fmt.Println(styleHeading.Render("Workloads"))
		fmt.Println(table.New().Border(lipgloss.NormalBorder()).Headers("ID", "Runtime", "Target").Rows(rows...).Render())
	}
	for _, workload := range workloads {
		fmt.Printf("\n%s (%s)\n", workload.ID, workload.Runtime)
		switch workload.Runtime {
		case "systemd":
			runCmd(Options{DryRun: opts.DryRun, Verbose: opts.Verbose}, "systemctl", "status", "--no-pager", workload.UnitName)
		case "docker":
			runCompose(opts, resolvePath(baseDir, manifest.Files.Compose), "ps", workload.ComposeService)
		}
	}
}

func runUninstall(manifest RuntimeManifest, baseDir string, opts Options) {
	workloads := filterWorkloads(manifest, opts.Workload)
	runStop(manifest, baseDir, opts)
	progress := newProgress(len(workloads) + 1)
	for _, workload := range workloads {
		if workload.Runtime != "systemd" {
			continue
		}
		progress.Step("disable and remove " + workload.UnitName)
		runCmd(opts, "systemctl", "disable", workload.UnitName)
		runRemove(filepath.Join("/etc/systemd/system", workload.UnitName), opts)
	}
	progress.Step("reload systemd")
	runCmd(opts, "systemctl", "daemon-reload")
}

type progress struct {
	total int
	done  int
}

func newProgress(total int) *progress {
	if total <= 0 {
		total = 1
	}
	return &progress{total: total}
}

func (p *progress) Step(message string) {
	p.done++
	if p.done > p.total {
		p.total = p.done
	}
	fmt.Printf("%s %s\n", styleInfo.Render(fmt.Sprintf("[%d/%d]", p.done, p.total)), message)
}

func shouldElevate(commandName string, manifest RuntimeManifest, opts Options) bool {
	if opts.NoElevate || isRoot() || runtime.GOOS != "linux" {
		return false
	}
	switch commandName {
	case "install", "uninstall":
		return true
	case "start", "stop", "restart":
		return hasRuntime(manifest, "systemd")
	default:
		return false
	}
}

func elevate(commandName string, opts Options) error {
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("%s requires root privileges; sudo is not available, please run as root", commandName)
	}
	if !opts.Yes {
		confirm := true
		if err := survey.AskOne(&survey.Confirm{
			Message: fmt.Sprintf("%s requires root privileges. Re-run with sudo?", commandName),
			Default: true,
		}, &confirm); err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("%s requires root privileges; run again with sudo or use --yes to allow automatic elevation", commandName)
		}
	}
	args := append([]string{os.Args[0]}, os.Args[1:]...)
	cmd := exec.Command("sudo", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func filterWorkloads(manifest RuntimeManifest, id string) []RuntimeWorkload {
	if id == "" {
		return manifest.Workloads
	}
	for _, workload := range manifest.Workloads {
		if workload.ID == id || workload.UnitName == id || workload.ComposeService == id {
			return []RuntimeWorkload{workload}
		}
	}
	exitError(fmt.Errorf("workload %q not found", id))
	return nil
}

func hasRuntime(manifest RuntimeManifest, runtimeName string) bool {
	return hasWorkloadRuntime(manifest.Workloads, runtimeName)
}

func hasWorkloadRuntime(workloads []RuntimeWorkload, runtimeName string) bool {
	for _, workload := range workloads {
		if workload.Runtime == runtimeName {
			return true
		}
	}
	return false
}

func composeCommand() []string {
	if _, err := exec.LookPath("docker"); err == nil {
		if exec.Command("docker", "compose", "version").Run() == nil {
			return []string{"docker", "compose"}
		}
	}
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return []string{"docker-compose"}
	}
	return nil
}

func runCompose(opts Options, composeFile string, args ...string) {
	base := composeCommand()
	if base == nil {
		exitError(errors.New("docker compose or docker-compose is required"))
	}
	fullArgs := append(base[1:], "-f", composeFile)
	fullArgs = append(fullArgs, args...)
	runCmd(opts, base[0], fullArgs...)
}

func runCmd(opts Options, name string, args ...string) {
	if opts.Verbose || opts.DryRun {
		fmt.Println("+", name, strings.Join(args, " "))
	}
	if opts.DryRun {
		return
	}
	spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	spin.Suffix = " " + name + " " + strings.Join(args, " ")
	if !opts.Verbose {
		spin.Start()
		defer spin.Stop()
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		exitError(err)
	}
}

func resolveInstallOptions(manifest RuntimeManifest, opts Options) RuntimeManifest {
	if opts.InstallDir != "" {
		manifest.InstallDir = opts.InstallDir
		return manifest
	}
	value := manifest.InstallDir
	if value == "" {
		value = "/opt/lsap"
	}
	if !opts.Yes {
		prompt := &survey.Input{
			Message: "Install directory:",
			Default: value,
		}
		if err := survey.AskOne(prompt, &value); err != nil {
			exitError(err)
		}
	}
	manifest.InstallDir = value
	return manifest
}

func confirmInstallTarget(manifest RuntimeManifest, opts Options) {
	if manifest.InstallDir == "" {
		exitError(errors.New("install directory is required"))
	}
	if opts.DryRun {
		fmt.Println(styleInfo.Render("install dir"), manifest.InstallDir)
		return
	}
	notEmpty, err := dirExistsNotEmpty(manifest.InstallDir)
	if err != nil {
		exitError(err)
	}
	if !notEmpty {
		return
	}
	if opts.Yes {
		fmt.Println(styleWarn.Render("warning"), "install directory exists and is not empty:", manifest.InstallDir)
		return
	}
	confirm := false
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Install directory %s exists and is not empty. Continue?", manifest.InstallDir),
		Default: false,
	}, &confirm); err != nil {
		exitError(err)
	}
	if !confirm {
		exitError(errors.New("install canceled"))
	}
}

func backupInstall(manifest RuntimeManifest, opts Options) {
	if manifest.InstallDir == "" || !pathExists(manifest.InstallDir) {
		return
	}
	if opts.Interactive && !opts.Yes {
		confirm := true
		if err := survey.AskOne(&survey.Confirm{
			Message: fmt.Sprintf("Backup existing install directory %s?", manifest.InstallDir),
			Default: true,
		}, &confirm); err != nil {
			exitError(err)
		}
		if !confirm {
			return
		}
	}
	root := opts.BackupDir
	if root == "" {
		root = filepath.Join(filepath.Dir(manifest.InstallDir), ".lsapctl-backups")
	}
	target := filepath.Join(root, filepath.Base(manifest.InstallDir)+"-"+time.Now().Format("20060102-150405"))
	fmt.Println(styleInfo.Render("backup"), manifest.InstallDir, "->", target)
	if opts.DryRun {
		return
	}
	if err := copyDir(manifest.InstallDir, target); err != nil {
		exitError(err)
	}
}

func copyDir(source string, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", source)
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, info.Mode())
		}
		return copyFile(path, destination, info.Mode())
	})
}

func copyFile(source string, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func severityLabel(severity string) string {
	switch severity {
	case "error":
		return styleError.Render("ERROR")
	case "warning":
		return styleWarn.Render("WARN")
	case "info":
		return styleInfo.Render("INFO")
	default:
		return severity
	}
}

func workloadTarget(workload RuntimeWorkload) string {
	if workload.Runtime == "systemd" {
		return workload.UnitName
	}
	if workload.Runtime == "docker" {
		return workload.ComposeService
	}
	return ""
}

func runCopy(source string, target string, opts Options) {
	if opts.Verbose || opts.DryRun {
		fmt.Printf("+ copy %s %s\n", source, target)
	}
	if opts.DryRun {
		return
	}
	content, err := os.ReadFile(source)
	if err != nil {
		exitError(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		exitError(err)
	}
}

func runRemove(target string, opts Options) {
	if opts.Verbose || opts.DryRun {
		fmt.Println("+ remove", target)
	}
	if opts.DryRun {
		return
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		exitError(err)
	}
}

func execCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func resolvePath(baseDir string, value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(baseDir, value)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExistsNotEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func isWritable(path string) bool {
	test := filepath.Join(path, ".lsapctl-write-test-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(test, []byte("test"), 0o600); err != nil {
		return false
	}
	_ = os.Remove(test)
	return true
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
