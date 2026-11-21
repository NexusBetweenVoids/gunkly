// Command gunkly is the Gunkly server and its operator CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	_ "time/tzdata" // distroless has no zoneinfo; households need IANA zones
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: gunkly <command> [arguments]

Commands:
  serve                       run the server (default)
  healthcheck                 probe the local server's /healthz (exit 0 when healthy)
  migrate                     apply pending database migrations
  backup [path|-]             write a consistent SQLite snapshot to path (default inside the
                              data directory) or to stdout with -
  admin reset-password <user> set a new random password (printed once), sign out everywhere
  admin reset-2fa <user>      remove the user's TOTP secret and recovery codes
  rotate-key                  re-encrypt stored secrets with the current secret key
  version                     print the version
`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "gunkly:", err)
		}
		os.Exit(1)
	}
}

// run dispatches a subcommand. Arguments are parsed with the flag package
// so every command rejects unknown flags.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	args = fs.Args()
	switch cmd {
	case "serve":
		return serve(ctx, stderr)
	case "healthcheck":
		return healthcheck(ctx, os.Getenv("GUNKLY_LISTEN_ADDR"))
	case "migrate":
		return withApp(ctx, stderr, func(a *app) error { return a.migrate(ctx) })
	case "backup":
		if len(args) > 1 {
			return errors.New("backup takes at most one argument")
		}
		dst := ""
		if len(args) == 1 {
			dst = args[0]
		}
		return withApp(ctx, stderr, func(a *app) error { return a.backup(ctx, dst, stdout) })
	case "admin":
		if len(args) != 2 {
			fs.Usage()
			return errors.New("usage: gunkly admin reset-password|reset-2fa <user>")
		}
		return withApp(ctx, stderr, func(a *app) error { return a.adminCommand(ctx, args[0], args[1], stdout) })
	case "rotate-key":
		return withApp(ctx, stderr, func(a *app) error { return a.rotateKey(ctx, stdout) })
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	case "help", "-h", "--help":
		fs.Usage()
		return nil
	}
	fs.Usage()
	return fmt.Errorf("unknown command %q", cmd)
}
