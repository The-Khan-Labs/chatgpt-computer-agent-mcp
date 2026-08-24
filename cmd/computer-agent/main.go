package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"chatgpt-computer-agent-mcp/internal/command"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/files"
	agentmcp "chatgpt-computer-agent-mcp/internal/mcp"
	"chatgpt-computer-agent-mcp/internal/policy"
	"chatgpt-computer-agent-mcp/internal/processes"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) > 0 && arguments[0] == "configure" {
		return runConfigure(arguments[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("computer-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the JSON configuration file")
	showVersion := flags.Bool("version", false, "print the program version")
	showHelp := flags.Bool("help", false, "show help")
	shortHelp := flags.Bool("h", false, "show help")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *showHelp || *shortHelp {
		if err := usage(stdout); err != nil {
			return 1
		}
		return 0
	}
	if *showVersion {
		if _, err := fmt.Fprintf(stdout, "computer-agent %s\n", version); err != nil {
			return 1
		}
		return 0
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected positional argument %q; use --help for usage\n", flags.Arg(0))
		return 2
	}
	if *configPath == "" {
		path, err := config.DefaultPath()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "configuration error: cannot determine the default path: %v; pass --config <path>\n", err)
			return 1
		}
		*configPath = path
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error: %v; create a valid file there or pass --config <path>\n", err)
		return 1
	}
	roots, err := policy.Open(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error: %v; correct the approved roots in %s\n", err, cfg.Path())
		return 1
	}
	fileService := files.New(roots, cfg.Limits())
	launcher := command.New(roots, cfg.Limits())
	registry := processes.New(launcher, cfg.Limits().MaxBackgroundProcesses)
	server := agentmcp.New(version, roots, fileService, launcher, registry, cfg.Limits())

	ctx, stopSignals := notifyContext()
	err = server.RunStdio(ctx)
	stopSignals()
	err = errors.Join(err, roots.Close())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "server error: %v\n", err)
		return 1
	}
	return 0
}

func runConfigure(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("computer-agent configure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the JSON configuration file")
	mode := flags.String("mode", "", "permission mode: readonly, workspace, or user-shell")
	root := flags.String("root", "", "absolute path to the approved workspace directory")
	showHelp := flags.Bool("help", false, "show help")
	shortHelp := flags.Bool("h", false, "show help")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *showHelp || *shortHelp {
		if err := configureUsage(stdout); err != nil {
			return 1
		}
		return 0
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected positional argument %q; use computer-agent configure --help for usage\n", flags.Arg(0))
		return 2
	}
	if *mode == "" {
		_, _ = fmt.Fprintln(stderr, "--mode is required: readonly, workspace, or user-shell")
		return 2
	}
	selected := config.Mode(*mode)
	if selected != config.Readonly && selected != config.Workspace && selected != config.UserShell {
		_, _ = fmt.Fprintf(stderr, "invalid mode %q; supported modes are readonly, workspace, and user-shell\n", *mode)
		return 2
	}
	if *configPath == "" {
		path, err := config.DefaultPath()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "configuration error: cannot determine the default path: %v; pass --config <path>\n", err)
			return 1
		}
		*configPath = path
	}
	result, err := config.Configure(*configPath, selected, *root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, configureReport(result)); err != nil {
		return 1
	}
	return 0
}

func configureReport(result *config.ConfigureResult) string {
	var report strings.Builder
	report.WriteString("Configured ChatGPT Computer Agent MCP\n\n")
	fmt.Fprintf(&report, "Mode: %s\n", result.Config.Mode())
	for _, root := range result.Config.Roots() {
		if root.Name == config.WorkspaceRootName {
			fmt.Fprintf(&report, "Approved workspace: %s\n", root.Path)
		} else {
			fmt.Fprintf(&report, "Approved root %s: %s\n", root.Name, root.Path)
		}
	}
	fmt.Fprintf(&report, "Config: %s\n\n", result.Config.Path())
	if result.Config.Mode() == config.UserShell {
		report.WriteString("user-shell allows arbitrary commands as your current OS user. Approved roots constrain file tools but do not sandbox commands.\n")
	}
	report.WriteString("Restart tunnel-client for permission changes to take effect.\n")
	return report.String()
}

func usage(writer io.Writer) error {
	if _, err := fmt.Fprintln(writer, "Usage: computer-agent [--config <path>] [--version] [--help]"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "       computer-agent configure --mode <readonly|workspace|user-shell> [--root <path>] [--config <path>]"); err != nil {
		return err
	}
	_, err := fmt.Fprintln(writer, "Runs the ChatGPT Computer Agent MCP server over stdio.")
	return err
}

func configureUsage(writer io.Writer) error {
	lines := []string{
		"Usage: computer-agent configure --mode <readonly|workspace|user-shell> [--root <path>] [--config <path>]",
		"Creates or updates the configuration without manual JSON editing.",
		"First-time configuration requires --root, an existing absolute directory.",
		"With an existing configuration, only the mode (and, with --root, the",
		"\"workspace\" root) is changed; other roots and limits are preserved.",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}
