package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/commerce/instacart/internal/auth"
	"github.com/mvanhorn/printing-press-library/library/commerce/instacart/internal/config"
	"github.com/mvanhorn/printing-press-library/library/commerce/instacart/internal/store"
)

// Exit codes used throughout the CLI.
const (
	ExitOK        = 0
	ExitUsage     = 2
	ExitAuth      = 3
	ExitNotFound  = 4
	ExitConflict  = 5
	ExitTransient = 7
)

type CodedError struct {
	msg  string
	code int
}

func (e CodedError) Error() string { return e.msg }
func (e CodedError) Code() int     { return e.code }

func coded(code int, format string, args ...any) CodedError {
	return CodedError{msg: fmt.Sprintf(format, args...), code: code}
}

// AppContext is the shared context passed to each command.
type AppContext struct {
	Ctx     context.Context
	Cfg     *config.Config
	Store   *store.Store
	Session *auth.Session
	JSON    bool
	DryRun  bool
}

// newAppContext loads config + store. Session is loaded lazily because auth
// commands need to run without one.
func newAppContext(cmd *cobra.Command) (*AppContext, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	// PATCH (instacart-address-profiles): per-call `--profile <name>`
	// override copies the named profile's location onto the in-memory cfg
	// so every downstream gql call uses it. Does NOT persist — switching
	// the active profile is `config profiles use`.
	if profileName, _ := cmd.Flags().GetString("profile"); profileName != "" {
		if err := cfg.ApplyProfile(profileName); err != nil {
			return nil, coded(ExitNotFound, "%v (run `instacart config profiles list` to see available profiles)", err)
		}
	}
	st, err := store.Open()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	// Cancel on SIGINT so in-flight HTTP calls stop quickly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	jsonOut, _ := cmd.Flags().GetBool("json")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	return &AppContext{
		Ctx:    ctx,
		Cfg:    cfg,
		Store:  st,
		JSON:   jsonOut,
		DryRun: dryRun,
	}, nil
}

func (a *AppContext) RequireSession() error {
	if a.Session != nil {
		return nil
	}
	sess, err := auth.LoadSession()
	if err != nil {
		return coded(ExitAuth, "%v", err)
	}
	a.Session = sess
	return nil
}

// stderr returns os.Stderr. Wrapped in a method so tests can swap it.
func (a *AppContext) stderr() *os.File { return os.Stderr }

// Version is set at build time via -ldflags or defaults to "dev".
var Version = "1.0.0"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:     "instacart",
		Short:   "Agent-native Instacart CLI. Manage your cart, search products, and shop at your favorite retailers from the command line.",
		Version: Version,
		Long: `instacart is a single-binary command line client for Instacart. It uses the
session you already have in Chrome (via kooky), talks directly to Instacart's
GraphQL endpoint, and gives agents a fast, scriptable surface for real cart
operations: search, add, remove, show carts across retailers.

No browser automation. No Playwright. No Composio subscription. Just a binary.`,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().Bool("json", false, "Output machine-readable JSON instead of pretty text")
	root.PersistentFlags().Bool("dry-run", false, "Show what would happen without making network calls or writes")
	root.PersistentFlags().Bool("verbose", false, "Verbose debug output")
	// PATCH (instacart-address-profiles): per-call address override.
	root.PersistentFlags().String("profile", "", "Use a named address profile for this call (see `instacart config profiles list`)")

	root.AddCommand(
		newDoctorCmd(),
		newAuthCmd(),
		// PATCH (fix-instacart-location-config-546): config subtree.
		newConfigCmd(),
		newRetailersCmd(),
		newSearchCmd(),
		newAddCmd(),
		newCartCmd(),
		newCartsCmd(),
		newCaptureCmd(),
		newOpsCmd(),
		newHistoryCmd(),
	)

	learnCfg := newLearnConfig()
	var learnFlags rootFlags
	root.PersistentFlags().BoolVar(&learnFlags.noLearn, "no-learn", false, "Disable the teach/recall learning loop for this invocation")
	root.AddCommand(newTeachCmd(&learnFlags, learnCfg))
	root.AddCommand(newRecallCmd(&learnFlags, learnCfg))
	root.AddCommand(newLearningsCmd(&learnFlags, learnCfg))
	root.AddCommand(newTeachPatternCmd(&learnFlags))
	root.AddCommand(newTeachLookupCmd(&learnFlags))
	return root
}

// learnHookSkipList enumerates framework command names that any
// future PersistentPreRunE recall hook must NOT trigger on. Today the
// teach/recall path is invoked explicitly by the agent, so there is
// no consumer of this list at runtime; the skip-list ships in v1 as
// forward-looking framework so a later auto-recall hook can consult
// it without re-deriving the set in every PR.
//
// Names match the cobra Use: field. Aliases are matched as-is.
var learnHookSkipList = map[string]struct{}{
	"auth":          {},
	"doctor":        {},
	"help":          {},
	"sync":          {},
	"profile":       {},
	"feedback":      {},
	"which":         {},
	"agent-context": {},
	"completion":    {},
	"version":       {},
}

// shouldSkipLearnHook reports whether a recall pre-run hook should
// short-circuit for cmdName. Used today only by unit tests asserting
// the contents of learnHookSkipList; reserved for a future
// PersistentPreRunE auto-recall integration.
func shouldSkipLearnHook(cmdName string) bool {
	_, skip := learnHookSkipList[cmdName]
	return skip
}
