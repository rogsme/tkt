package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/service"
)

// ServiceResult is what tkt watch --install or --uninstall did. Unit is the
// repository's tkt watch service: a systemd unit or a launchd label. Herdr
// says how herdr's server runs: started (tkt started its service), service
// (its service ran already), running (it runs outside its service, which
// takes over at the next boot or login), or off (linger is off, so its unit
// stays off: systemd would stop it, and every agent in it, with your last
// session). Linger says whether systemd keeps the services past your last
// session and starts them at boot (yes) or not (no); launchd starts them
// at login, and leaves it "".
type ServiceResult struct {
	Manager string   `json:"manager"`
	Unit    string   `json:"unit"`
	Files   []string `json:"files"`
	Removed bool     `json:"removed"`
	Herdr   string   `json:"herdr"`
	Linger  string   `json:"linger"`
}

func (s *ServiceResult) text(repo string, dry bool) string {
	switch {
	case s.Removed && dry:
		return fmt.Sprintf("would remove %s\n", s.Unit)
	case s.Removed:
		return fmt.Sprintf("removed %s, so tkt watch for %s no longer runs as a service\n", s.Unit, repo)
	case dry:
		return fmt.Sprintf("would run tkt watch for %s as %s, writing %s\n", repo, s.Unit, strings.Join(s.Files, " and "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s runs tkt watch for %s\n", s.Unit, repo)
	for _, f := range s.Files {
		fmt.Fprintf(&b, "wrote %s\n", f)
	}
	switch s.Herdr {
	case "started":
		b.WriteString("started herdr's server as a service\n")
	case "running":
		b.WriteString("herdr runs outside its service, which takes over at the next start\n")
	case "off":
		b.WriteString("herdr's unit stays off while linger is off, so herdr runs as you start it\n")
	}
	switch s.Linger {
	case "yes":
		b.WriteString("the services start at boot, and outlive your sessions\n")
	case "no":
		b.WriteString("tkt watch runs from login to logout\n")
	}
	return b.String()
}

// watchSvc is a repository's tkt watch service, whether or not it is
// installed: its manager, its unit or label, herdr's, and the two files
// that define them, herdr's first.
type watchSvc struct {
	manager string
	unit    string
	herdr   string
	files   []string
	domain  string
}

// serviceManager names the service manager tkt writes for: launchd on
// macOS, systemd elsewhere when systemctl is installed, or "" when there is
// none. TKT_SERVICE_MANAGER picks one, or none with any other value.
func serviceManager(getenv func(string) string) string {
	switch m := getenv("TKT_SERVICE_MANAGER"); m {
	case service.Systemd, service.Launchd:
		return m
	case "":
	default:
		return ""
	}
	if runtime.GOOS == "darwin" {
		return service.Launchd
	}
	if found("systemctl") {
		return service.Systemd
	}
	return ""
}

// watchService finds the repository's tkt watch service.
func (a *App) watchService(ctx context.Context, root string) (watchSvc, error) {
	switch m := serviceManager(a.Getenv); m {
	case service.Systemd:
		r := a.R.Run(ctx, runner.Cmd{Argv: service.EscapeArgv(root), Timeout: 10 * time.Second})
		esc := strings.TrimSpace(r.Out())
		if !r.OK() || esc == "" {
			return watchSvc{}, apperr.New(apperr.ServiceFailed, "systemd-escape --path failed: %s", oneLine(strings.TrimSpace(r.ErrText())))
		}
		dir := service.UnitDir(a.Getenv)
		return watchSvc{manager: m, unit: service.WatchUnit(esc), herdr: service.HerdrUnit,
			files: []string{filepath.Join(dir, service.HerdrUnit), filepath.Join(dir, service.WatchTemplate)}}, nil
	case service.Launchd:
		label := service.WatchLabel(root)
		dir := service.AgentDir(a.Getenv("HOME"))
		return watchSvc{manager: m, unit: label, herdr: service.HerdrLabel, domain: service.Domain(os.Getuid()),
			files: []string{filepath.Join(dir, service.HerdrLabel+".plist"), filepath.Join(dir, label+".plist")}}, nil
	}
	return watchSvc{}, apperr.New(apperr.ServiceUnsupported, "found neither systemd nor launchd to run tkt watch as a service").
		WithHint("tkt commands in a herdr pane start tkt watch by themselves")
}

// enabled reports whether a unit, or launchd agent, starts by itself: at
// boot or login. A launchd agent does once its plist is in place.
func (a *App) enabled(ctx context.Context, s watchSvc, unit string) bool {
	if s.manager == service.Launchd {
		file := s.files[0]
		if unit != s.herdr {
			file = s.files[1]
		}
		_, err := os.Stat(file)
		return err == nil
	}
	r := a.R.Run(ctx, runner.Cmd{Argv: service.SystemctlArgv("is-enabled", unit), Timeout: 10 * time.Second})
	return strings.TrimSpace(r.Out()) == "enabled"
}

// loaded reports whether launchd knows an agent, running or not.
func (a *App) loaded(ctx context.Context, s watchSvc, unit string) bool {
	return a.R.Run(ctx, runner.Cmd{Argv: service.LaunchctlArgv("print", s.domain+"/"+unit), Timeout: 10 * time.Second}).OK()
}

// active reports whether a unit, or launchd agent, runs now.
func (a *App) active(ctx context.Context, s watchSvc, unit string) bool {
	if s.manager == service.Launchd {
		r := a.R.Run(ctx, runner.Cmd{Argv: service.LaunchctlArgv("print", s.domain+"/"+unit), Timeout: 10 * time.Second})
		return r.OK() && strings.Contains(r.Out(), "state = running")
	}
	r := a.R.Run(ctx, runner.Cmd{Argv: service.SystemctlArgv("is-active", unit), Timeout: 10 * time.Second})
	switch strings.TrimSpace(r.Out()) {
	case "active", "activating", "reloading":
		return true
	}
	return false
}

// installWatch writes the services that run herdr's server and this
// repository's tkt watch, enables both, and starts what does not run yet.
// The units carry this shell's PATH: agents in herdr's panes inherit it.
func (c *Ctx) installWatch(o WatchOpts) (*WatchResult, error) {
	if err := c.LC.RequireGitHub("watch"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh", "herdr"); err != nil {
		return nil, err
	}
	s, err := c.watchService(c.Ctx, c.Repo.Root)
	if err != nil {
		return nil, err
	}
	spec, err := c.serviceSpec()
	if err != nil {
		return nil, err
	}
	sr := &ServiceResult{Manager: s.manager, Unit: s.unit, Files: s.files}
	if s.manager == service.Systemd {
		err = c.installSystemd(s, spec, sr, o.Yes)
	} else {
		err = c.installLaunchd(s, spec, sr)
	}
	if err != nil {
		return nil, err
	}
	return &WatchResult{Repo: c.Repo.Root, Tickets: []WatchTicket{}, Service: sr, DryRun: c.Dry, Actions: append([]Action{}, c.Plan...)}, nil
}

// serviceSpec is what the services run: the tkt on PATH, which tkt update
// replaces in place, herdr, and this shell's PATH.
func (c *Ctx) serviceSpec() (service.Spec, error) {
	tkt, err := exec.LookPath("tkt")
	if err != nil {
		return service.Spec{}, apperr.New(apperr.ToolMissing, "tkt is not on PATH, and the service runs the tkt your shell finds").
			WithHint("put tkt on PATH, then run tkt watch --install again")
	}
	tkt, _ = filepath.Abs(tkt)
	if self, err := os.Executable(); err == nil && !sameFile(self, tkt) {
		c.UI.Warn("the service runs %s, the tkt on PATH, not this one (%s)", tkt, self)
	}
	h, _ := exec.LookPath("herdr")
	h, _ = filepath.Abs(h)
	for _, p := range c.Cfg.Presets {
		if !found(p.Harness) {
			c.UI.Warn("this PATH finds no %s, so the agents tkt watch starts for presets that use it fail", p.Harness)
		}
	}
	return service.Spec{Herdr: h, Tkt: tkt, Path: c.Getenv("PATH")}, nil
}

func sameFile(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}

func (c *Ctx) installSystemd(s watchSvc, spec service.Spec, sr *ServiceResult, yes bool) error {
	if err := c.writeServiceFile(s.files[0], service.HerdrUnitFile(spec)); err != nil {
		return err
	}
	if err := c.writeServiceFile(s.files[1], service.WatchUnitFile(spec)); err != nil {
		return err
	}
	if err := c.serviceRun(service.SystemctlArgv("daemon-reload")); err != nil {
		return err
	}
	// Without linger, systemd stops a user's units when the last session
	// ends, and herdr's would take every agent with it. herdr then stays as
	// you run it, which outlives a logout.
	on, err := c.linger(sr, yes)
	if err != nil {
		return err
	}
	if on {
		if err := c.serviceRun(service.SystemctlArgv("enable", s.herdr)); err != nil {
			return err
		}
		if err := c.startHerdr(s, sr, service.SystemctlArgv("start", s.herdr)); err != nil {
			return err
		}
	} else {
		sr.Herdr = "off"
		c.UI.Warn("linger is off, and systemd stops a user's units when the last session ends, so a herdr under it would take every agent with it; %s stays off, and herdr runs as you start it. loginctl enable-linger, then tkt watch --install, turns it on", s.herdr)
	}
	c.stopLoneWatch(s)
	for _, argv := range [][]string{service.SystemctlArgv("enable", s.unit), service.SystemctlArgv("restart", s.unit)} {
		if err := c.serviceRun(argv); err != nil {
			return err
		}
	}
	return nil
}

func (c *Ctx) installLaunchd(s watchSvc, spec service.Spec, sr *ServiceResult) error {
	sr.Linger = ""
	herdrLog := filepath.Join(c.Getenv("HOME"), "Library", "Logs", "herdr.log")
	watchLog := filepath.Join(c.Repo.Common, "tkt", "watch.log")
	if err := c.writeServiceFile(s.files[0], service.HerdrPlist(spec, herdrLog)); err != nil {
		return err
	}
	if err := c.writeServiceFile(s.files[1], service.WatchPlist(spec, s.unit, c.Repo.Root, watchLog)); err != nil {
		return err
	}
	// launchd refuses to load an agent twice; one that is loaded but
	// stopped, as after herdr server stop, starts with kickstart.
	start := service.LaunchctlArgv("bootstrap", s.domain, s.files[0])
	if c.loaded(c.Ctx, s, s.herdr) {
		start = service.LaunchctlArgv("kickstart", s.domain+"/"+s.herdr)
	}
	if err := c.startHerdr(s, sr, start); err != nil {
		return err
	}
	c.stopLoneWatch(s)
	// bootout fails when the agent is not loaded yet, which is fine: the
	// bootstrap that follows loads the plist just written.
	c.serviceTry(service.LaunchctlArgv("bootout", s.domain+"/"+s.unit))
	return c.serviceRun(service.LaunchctlArgv("bootstrap", s.domain, s.files[1]))
}

// startHerdr starts herdr's service, unless herdr's server runs already:
// a second server would only fail. One that runs outside the service keeps
// running, and the service takes over at the next boot or login.
func (c *Ctx) startHerdr(s watchSvc, sr *ServiceResult, argv []string) error {
	switch {
	case c.active(c.Ctx, s, s.herdr):
		sr.Herdr = "service"
		return nil
	case c.herdrUp():
		sr.Herdr = "running"
		c.UI.Info("herdr's server already runs outside %s, which takes over at the next start. To move it now, run herdr server stop, then %s, from a terminal outside herdr while no agent works", s.herdr, runner.Display(argv))
		return nil
	}
	sr.Herdr = "started"
	return c.serviceRun(argv)
}

// herdrUp reports whether herdr's server answers.
func (c *Ctx) herdrUp() bool {
	_, err := herdr.Client{R: c.R}.Panes(c.Ctx)
	return err == nil
}

// stopLoneWatch stops a tkt watch that runs outside the service, such as
// one a tkt command started, so the service's can take the lock.
func (c *Ctx) stopLoneWatch(s watchSvc) {
	if !c.watchRunning() || c.active(c.Ctx, s, s.unit) {
		return
	}
	if _, err := c.signalWatch(); err != nil {
		c.UI.Warn("%s", apperr.As(err).Msg)
	}
}

// linger asks systemd to keep the user's units past their last session,
// and to start them at boot, after asking. --yes skips the question;
// without a terminal and without --yes, the answer is no. It reports
// whether linger is on, or, in a dry run, would be.
func (c *Ctx) linger(sr *ServiceResult, yes bool) (bool, error) {
	if c.lingers(c.Ctx) {
		sr.Linger = "yes"
		return true, nil
	}
	sr.Linger = "no"
	switch {
	case yes:
	case c.Dry || !c.StdinTTY:
		return false, nil
	default:
		fmt.Fprint(c.UI.Err, "tkt: keep herdr and tkt watch running after you log out, and start them at boot? This runs loginctl enable-linger; without it, herdr stays out of systemd. [y/N] ")
		var answer string
		fmt.Fscanln(c.Stdin, &answer)
		if !strings.HasPrefix(strings.ToLower(answer), "y") {
			return false, nil
		}
	}
	if err := c.serviceRun([]string{"loginctl", "enable-linger"}); err != nil {
		return false, err
	}
	if !c.Dry {
		sr.Linger = "yes"
	}
	return true, nil
}

// lingers reports whether systemd keeps the user's units past their last
// session.
func (a *App) lingers(ctx context.Context) bool {
	name := a.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	r := a.R.Run(ctx, runner.Cmd{Argv: []string{"loginctl", "show-user", name, "--property=Linger", "--value"}, Timeout: 10 * time.Second})
	return strings.TrimSpace(r.Out()) == "yes"
}

// uninstallWatch stops and disables the repository's tkt watch service.
// herdr's service and the systemd template stay: other repositories use
// them.
func (c *Ctx) uninstallWatch() (*WatchResult, error) {
	s, err := c.watchService(c.Ctx, c.Repo.Root)
	if err != nil {
		return nil, err
	}
	res := &WatchResult{Repo: c.Repo.Root, Tickets: []WatchTicket{}, DryRun: c.Dry, Actions: []Action{}}
	if !c.enabled(c.Ctx, s, s.unit) && !c.active(c.Ctx, s, s.unit) {
		c.UI.Info("tkt watch for %s does not run as a service", c.Repo.Root)
		return res, nil
	}
	sr := &ServiceResult{Manager: s.manager, Unit: s.unit, Files: []string{}, Removed: true}
	if s.manager == service.Systemd {
		if err := c.serviceRun(service.SystemctlArgv("disable", "--now", s.unit)); err != nil {
			return nil, err
		}
	} else {
		c.serviceTry(service.LaunchctlArgv("bootout", s.domain+"/"+s.unit))
		if c.Dry {
			c.Would([]string{"rm", "-f", s.files[1]})
		} else if err := os.Remove(s.files[1]); err != nil && !os.IsNotExist(err) {
			return nil, apperr.New(apperr.ServiceFailed, "cannot remove %s: %v", s.files[1], err)
		}
		sr.Files = []string{s.files[1]}
	}
	c.UI.Info("%s stays for herdr's server; tkt watch still starts with the next tkt command in a herdr pane", s.herdr)
	res.Service, res.Actions = sr, append([]Action{}, c.Plan...)
	return res, nil
}

// writeServiceFile writes a unit or plist, or says so in a dry run.
func (c *Ctx) writeServiceFile(path, content string) error {
	if c.Dry {
		c.Would([]string{"tee", path})
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.New(apperr.ServiceFailed, "cannot create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return apperr.New(apperr.ServiceFailed, "cannot write %s: %v", path, err)
	}
	return nil
}

// serviceRun runs a service manager's command, or says so in a dry run.
func (c *Ctx) serviceRun(argv []string) error {
	if c.Dry {
		c.Would(argv)
		return nil
	}
	r := c.R.Run(c.Ctx, runner.Cmd{Argv: argv, Timeout: 2 * time.Minute})
	if !r.OK() {
		return apperr.New(apperr.ServiceFailed, "%s failed: %s", runner.Display(argv), oneLine(strings.TrimSpace(r.ErrText()))).With("argv", argv)
	}
	return nil
}

// serviceTry runs a command whose failure changes nothing.
func (c *Ctx) serviceTry(argv []string) {
	if c.Dry {
		c.Would(argv)
		return
	}
	c.R.Run(c.Ctx, runner.Cmd{Argv: argv, Timeout: time.Minute})
}

// startWatchService starts the repository's installed tkt watch service.
// It reports false when there is none, so the caller starts a watch of its
// own.
func (c *Ctx) startWatchService() bool {
	s, err := c.watchService(c.Ctx, c.Repo.Root)
	if err != nil || !c.enabled(c.Ctx, s, s.unit) {
		return false
	}
	argv := service.SystemctlArgv("start", "--no-block", s.unit)
	if s.manager == service.Launchd {
		argv = service.LaunchctlArgv("bootstrap", s.domain, s.files[1])
		if c.loaded(c.Ctx, s, s.unit) {
			argv = service.LaunchctlArgv("kickstart", s.domain+"/"+s.unit)
		}
	}
	if r := c.R.Run(c.Ctx, runner.Cmd{Argv: argv, Timeout: 30 * time.Second}); !r.OK() {
		c.UI.Warn("cannot start %s: %s", s.unit, oneLine(strings.TrimSpace(r.ErrText())))
		return true
	}
	c.UI.Info("started %s, which keeps the repository's tickets moving (tkt help watch)", s.unit)
	return true
}

// serviceCheck is tkt doctor's view of the services: whether the
// repository's tkt watch runs as one, and whether the services' PATH finds
// the tools and the harnesses the agents need.
func (a *App) serviceCheck(ctx context.Context, root string, harnesses []string) Check {
	const id = "services"
	if root == "" {
		return Check{ID: id, Status: CheckSkip, Message: "not inside a repository"}
	}
	s, err := a.watchService(ctx, root)
	if err != nil {
		return Check{ID: id, Status: CheckSkip, Message: "no systemd or launchd here; tkt commands in herdr panes start tkt watch"}
	}
	if !a.enabled(ctx, s, s.unit) {
		return Check{ID: id, Status: CheckSkip, Message: "tkt watch runs when a tkt command in a herdr pane starts it",
			Hint: "tkt watch --install runs it and herdr as services that start at boot"}
	}
	b, _ := os.ReadFile(s.files[1])
	path := service.UnitPath(string(b))
	start := "systemctl --user start " + s.unit
	if s.manager == service.Launchd {
		path = service.PlistPath(string(b))
		start = "launchctl bootstrap " + s.domain + " " + s.files[1]
	}
	var missing []string
	for _, t := range append([]string{"git", "wt", "gh", "herdr"}, harnesses...) {
		if !lookIn(path, t) {
			missing = append(missing, t)
		}
	}
	lingers := s.manager == service.Launchd || a.lingers(ctx)
	herdrOn := a.enabled(ctx, s, s.herdr)
	switch {
	case !a.active(ctx, s, s.unit):
		return Check{ID: id, Status: CheckWarn, Message: s.unit + " is installed but does not run", Hint: start}
	case len(missing) > 0:
		return Check{ID: id, Status: CheckWarn, Message: "the services' PATH finds no " + strings.Join(missing, ", "),
			Hint: "run tkt watch --install again from a shell whose PATH finds them"}
	case herdrOn && !lingers:
		return Check{ID: id, Status: CheckWarn, Message: s.herdr + " is on while linger is off, so it stops with your last session, and every agent with it",
			Hint: "loginctl enable-linger"}
	case !herdrOn && lingers:
		return Check{ID: id, Status: CheckWarn, Message: s.unit + " runs, but herdr's server does not start with it",
			Hint: "tkt watch --install"}
	}
	msg := s.unit + " runs tkt watch"
	switch {
	case !lingers:
		msg += " from login to logout, and herdr runs as you start it, since linger is off"
	case !a.active(ctx, s, s.herdr):
		msg += "; herdr runs outside " + s.herdr + " until its next start"
	}
	return Check{ID: id, Status: CheckOK, Message: msg}
}

// lookIn reports whether a PATH finds an executable named tool.
func lookIn(path, tool string) bool {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, tool)); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}
