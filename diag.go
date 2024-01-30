// Copyright (c) 2018
// Author: Jeff Weisberg <jaw @ tcp4me.com>
// Created: 2018-Ju8-24 14:01 (EDT)
// Function: AC style diagnostics+logging

/*
in config file:
    debug section

at top of file:
    var dl = diag.Logger("section")

in code:
    dl.Debug(...)
    dl.Verbose(...)
    ...
*/

// diagnostics + logging
package diag

import (
	"context"
	"flag"
	"fmt"
	"log/syslog"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"
)

// defaults
const (
	stackMax  = 1048576
	rateLimit = time.Minute
)

var hostname = "?"
var progname = "?"

var lock sync.RWMutex
var config = &Config{}
var mailSent = make(map[string]time.Time)
var defaultDiag = &Diag{section: "default", useStderr: true, uplevel: 3}
var slog *syslog.Writer

type Diag struct {
	section   string
	uplevel   int
	mailTo    string
	mailFrom  string
	progname  string
	debugAll  bool
	useStderr bool
}

// Config configures the loggger
type Config struct {
	MailTo        string
	MailFrom      string
	MailRateLimit time.Duration
	Sendmail      string
	Facility      string
	ProgName      string
	Debug         map[string]bool
}

type logconf struct {
	logprio   syslog.Priority
	toStderr  bool
	toEmail   bool
	withInfo  bool
	withTrace bool
}

func init() {
	flag.BoolVar(&defaultDiag.debugAll, "D", false, "enable all debugging")

	hostname, _ = os.Hostname()
	prog, _ := os.Executable()
	progname = path.Base(prog)
}

// WithMailTo changes the mail destination
func (d *Diag) WithMailTo(e string) *Diag {
	var n Diag
	n = *d
	n.mailTo = e
	return &n
}

// WithMailFrom changes the mail sender
func (d *Diag) WithMailFrom(e string) *Diag {
	var n Diag
	n = *d
	n.mailFrom = e
	return &n
}

// Verbose logs a message at verbose priority
func (d *Diag) Verbose(format string, args ...interface{}) {
	diag(logconf{
		logprio:  syslog.LOG_INFO,
		toStderr: true,
	}, d, format, args)
}

// Debug logs a message at debug priority
func (d *Diag) Debug(format string, args ...interface{}) {

	var cf = getConfig()

	if !d.debugAll && !cf.Debug[d.section] && !cf.Debug["all"] {
		return
	}

	diag(logconf{
		logprio:  syslog.LOG_DEBUG,
		toStderr: true,
		withInfo: true,
	}, d, format, args)
}

// Problem logs a message indicating a problem
func (d *Diag) Problem(format string, args ...interface{}) {
	diag(logconf{
		logprio:  syslog.LOG_WARNING,
		toStderr: true,
		toEmail:  true,
		withInfo: true,
	}, d, format, args)
}

// Bug logs a message indicating a bug
func (d *Diag) Bug(format string, args ...interface{}) {
	diag(logconf{
		logprio:   syslog.LOG_ERR,
		toStderr:  true,
		toEmail:   true,
		withInfo:  true,
		withTrace: true,
	}, d, format, args)
}

// Fatal logs a message at high priority + terminates the program
func (d *Diag) Fatal(format string, args ...interface{}) {
	diag(logconf{
		logprio:   syslog.LOG_ERR,
		toStderr:  true,
		toEmail:   true,
		withInfo:  true,
		withTrace: true,
	}, d, format, args)

	os.Exit(-1)
}

// ################################################################

// Verbose logs a message at verbose priority
func Verbose(format string, args ...interface{}) {
	defaultDiag.Verbose(format, args...)
}

// Problem logs a message indicating a problem
func Problem(format string, args ...interface{}) {
	defaultDiag.Problem(format, args...)
}

// Bug logs a message indicating a bug
func Bug(format string, args ...interface{}) {
	defaultDiag.Bug(format, args...)
}

// Fatal logs a message at high priority + terminates the program
func Fatal(format string, args ...interface{}) {
	defaultDiag.Fatal(format, args...)
}

// ################################################################

// Logger creates a logger
func Logger(sect string) *Diag {
	return &Diag{section: sect, useStderr: true, uplevel: 2}
}

// Logger creates a logger
func (d *Diag) Logger(sect string) *Diag {
	n := *d
	n.section = sect
	return &n
}

// SetDebugAll sets the debugall flag
func (d *Diag) SetDebugAll(x bool) {
	d.debugAll = x
}

// SetStderr enables stderr output
func (d *Diag) SetStderr(x bool) {
	d.useStderr = x
}

// SetConfig sets the config
func SetConfig(cf Config) {
	lock.Lock()
	defer lock.Unlock()
	config = &cf

	if slog == nil {
		openSyslog(cf.Facility)
	}
}

func getConfig() *Config {
	lock.RLock()
	defer lock.RUnlock()
	return config
}

// SetDebugFlag enables/disables debug logging for a specified section
func SetDebugFlag(f string, v bool) {
	lock.Lock()
	defer lock.Unlock()

	config.Debug[f] = v
}

// ################################################################

func diag(cf logconf, d *Diag, format string, args []interface{}) {

	var out string

	if cf.withInfo {
		pc, file, line, ok := runtime.Caller(d.uplevel)
		if ok {
			// file is full pathname - trim
			fileshort := cleanFilename(file)

			// get function name
			fun := runtime.FuncForPC(pc)
			if fun != nil {
				funName := cleanFunName(fun.Name())
				out = fmt.Sprintf("%s:%d %s(): ", fileshort, line, funName)
			} else {
				out = fmt.Sprintf("%s:%d ?(): ", fileshort, line)
			}
		} else {
			out = "?:?: "
		}
	}

	// remove a trailing newline
	if format[len(format)-1] == '\n' {
		format = format[:len(format)-1]
	}

	out = out + fmt.Sprintf(format, args...)

	if cf.toStderr && d.useStderr {
		fmt.Fprintln(os.Stderr, out)
	}

	// syslog
	if slog != nil {
		sendToSyslog(cf.logprio, out)
	}

	// email
	if cf.toEmail {
		sendEmail(d, out, cf.withTrace)
	}

}

func sendEmail(d *Diag, txt string, withTrace bool) {

	cf := getConfig()

	if cf == nil {
		return
	}

	var dcf Diag
	dcf = *d

	if dcf.mailTo == "" {
		dcf.mailTo = cf.MailTo
	}
	if dcf.mailFrom == "" {
		dcf.mailFrom = cf.MailFrom
	}
	if dcf.progname == "" {
		dcf.progname = cf.ProgName
	}
	if dcf.progname == "" {
		dcf.progname = progname
	}

	if dcf.mailTo == "" || dcf.mailFrom == "" {
		return
	}

	if cf.rateLimited(dcf.mailTo) {
		return
	}

	sendmail := "sendmail"
	if cf.Sendmail != "" {
		sendmail = cf.Sendmail
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, sendmail, "-t", "-f", dcf.mailFrom)

	p, _ := cmd.StdinPipe()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Start()

	go func() {
		fmt.Fprintf(p, "To: %s\nFrom: %s\nSubject: %s daemon error\n\n",
			dcf.mailTo, dcf.mailFrom, dcf.progname)

		fmt.Fprintf(p, "an error was detected in %s\n\nhost:   %s\npid:    %d\n\n",
			dcf.progname, hostname, os.Getpid())

		fmt.Fprintf(p, "error:\n%s\n", txt)

		if withTrace {
			var stack = make([]byte, stackMax)
			stack = stack[:runtime.Stack(stack, true)]
			fmt.Fprintf(p, "\n\n%s\n", stack)
		}

		p.Close()
	}()

	cmd.Wait()
}

func sendToSyslog(prio syslog.Priority, msg string) {

	switch prio {
	case syslog.LOG_DEBUG:
		slog.Debug(msg)
	case syslog.LOG_INFO:
		slog.Info(msg)
	case syslog.LOG_NOTICE:
		slog.Notice(msg)
	case syslog.LOG_WARNING:
		slog.Warning(msg)
	case syslog.LOG_ERR:
		slog.Err(msg)
	case syslog.LOG_ALERT:
		slog.Alert(msg)
	case syslog.LOG_EMERG:
		slog.Emerg(msg)
	case syslog.LOG_CRIT:
		slog.Crit(msg)
	}
}

var prioName = map[string]syslog.Priority{
	"kern":     syslog.LOG_KERN,
	"user":     syslog.LOG_USER,
	"mail":     syslog.LOG_MAIL,
	"daemon":   syslog.LOG_DAEMON,
	"auth":     syslog.LOG_AUTH,
	"syslog":   syslog.LOG_SYSLOG,
	"lpr":      syslog.LOG_LPR,
	"news":     syslog.LOG_NEWS,
	"uucp":     syslog.LOG_UUCP,
	"cron":     syslog.LOG_CRON,
	"authpriv": syslog.LOG_AUTHPRIV,
	"ftp":      syslog.LOG_FTP,
	"local0":   syslog.LOG_LOCAL0,
	"local1":   syslog.LOG_LOCAL1,
	"local2":   syslog.LOG_LOCAL2,
	"local3":   syslog.LOG_LOCAL3,
	"local4":   syslog.LOG_LOCAL4,
	"local5":   syslog.LOG_LOCAL5,
	"local6":   syslog.LOG_LOCAL6,
	"local7":   syslog.LOG_LOCAL7,
}

func openSyslog(fac string) {

	p, ok := prioName[strings.ToLower(fac)]

	if !ok {
		return
	}

	slog, _ = syslog.New(p, progname)
}

// trim full pathname to dir/file.go
func cleanFilename(file string) string {

	si := strings.LastIndex(file, "/")

	if si == -1 {
		return file
	}

	ssi := strings.LastIndex(file[0:si-1], "/")
	if ssi != -1 {
		si = ssi
	}

	return file[si+1:]
}

func cleanFunName(n string) string {

	dot := strings.LastIndexByte(n, '.')
	if dot != -1 {
		n = n[dot+1:]
	}
	return n
}

func (cf *Config) rateLimited(addr string) bool {
	lock.Lock()
	defer lock.Unlock()

	now := time.Now()
	sent := mailSent[addr]

	limit := cf.MailRateLimit
	if limit == 0 {
		limit = rateLimit
	}

	if now.After(sent.Add(limit)) {
		mailSent[addr] = now
		return false
	}

	return true
}
