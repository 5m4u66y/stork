package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	storkutil "isc.org/stork/util"
)

// Represents the BIND 9 process metadata.
type Bind9Daemon struct {
	Pid     int32
	Name    string
	Version string
	Active  bool
}

// Represents the state of BIND 9.
type Bind9State struct {
	Version string
	Active  bool
	Daemon  Bind9Daemon
}

// Represents the RNDC key entry.
type Bind9RndcKey struct {
	Name      string
	Algorithm string
	Secret    string
}

// Returns the string representation of the key.
func (k *Bind9RndcKey) String() string {
	return fmt.Sprintf("%s:%s:%s", k.Name, k.Algorithm, k.Secret)
}

// It holds common and BIND 9 specific runtime information.
type Bind9App struct {
	BaseApp
	RndcClient *RndcClient // to communicate with BIND 9 via rndc
}

// Get base information about BIND 9 app.
func (ba *Bind9App) GetBaseApp() *BaseApp {
	return &ba.BaseApp
}

// Detect allowed logs provided by BIND 9.
// TODO: currently it is not implemented and not used,
// it returns always empty list and no error.
func (ba *Bind9App) DetectAllowedLogs() ([]string, error) {
	return nil, nil
}

// Returns a list of the configured daemons in a given application.
func (ba *Bind9App) GetConfiguredDaemons() []string {
	// Bind9 is a single daemon application.
	return []string{"named"}
}

// List of BIND 9 executables used during app detection.
const (
	namedCheckconfExec = "named-checkconf"
	rndcExec           = "rndc"
	namedExec          = "named"
)

// rndc key file name.
const RndcKeyFile = "rndc.key"

// Default ports for rndc and stats channel.
const (
	RndcDefaultPort         = 953
	StatsChannelDefaultPort = 80
)

// Object for interacting with named using rndc.
type RndcClient struct {
	execute     CommandExecutor
	BaseCommand []string
}

// CommandExecutor takes an array of strings, with the first element of the
// array being the program to call, followed by its arguments.  It returns
// the command output, and possibly an error (for example if running the
// command failed).
type CommandExecutor func([]string) ([]byte, error)

// Create an rndc client to communicate with BIND 9 named daemon.
func NewRndcClient(ce CommandExecutor) *RndcClient {
	rndcClient := &RndcClient{
		execute: ce,
	}
	return rndcClient
}

// Determine rndc details in the system.
// It find rndc executable and prepare base command with all necessary
// parameters including rndc secret key.
func (rc *RndcClient) DetermineDetails(baseNamedDir, bind9ConfDir string, ctrlAddress string, ctrlPort int64, ctrlKey *Bind9RndcKey, executor storkutil.CommandExecutor) error {
	rndcPath, err := determineBinPath(baseNamedDir, rndcExec, executor)
	if err != nil {
		return err
	}

	cmd := []string{rndcPath, "-s", ctrlAddress, "-p", fmt.Sprintf("%d", ctrlPort)}

	if ctrlKey != nil {
		cmd = append(cmd, "-y")
		cmd = append(cmd, ctrlKey.Name)
	} else {
		keyPath := path.Join(bind9ConfDir, RndcKeyFile)
		if _, err = os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "cannot determine rndc key")
		}
		cmd = append(cmd, "-k")
		cmd = append(cmd, keyPath)
	}
	rc.BaseCommand = cmd
	return nil
}

// Send command to named using rndc executable.
func (rc *RndcClient) SendCommand(command []string) (output []byte, err error) {
	var rndcCommand []string
	rndcCommand = append(rndcCommand, rc.BaseCommand...)
	rndcCommand = append(rndcCommand, command...)
	log.Debugf("Rndc: %+v", rndcCommand)

	return rc.execute(rndcCommand)
}

// getRndcKey looks for the key with a given `name` in `contents`.
//
// Example key clause:
//
//	key "name" {
//		algorithm "hmac-sha256";
//		secret "OmItW1lOyLVUEuvv+Fme+Q==";
//	};
func getRndcKey(contents, name string) (controlKey *Bind9RndcKey) {
	pattern := regexp.MustCompile(`(?s)key\s+\"(\S+)\"\s+\{(.*?)\}\s*;`)
	keys := pattern.FindAllStringSubmatch(contents, -1)
	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		if key[1] != name {
			continue
		}
		pattern = regexp.MustCompile(`(?s)algorithm\s+\"(\S+)\";`)
		algorithm := pattern.FindStringSubmatch(key[2])
		if len(algorithm) < 2 {
			log.Warnf("No key algorithm found for name %s", name)
			return nil
		}

		pattern = regexp.MustCompile(`(?s)secret\s+\"(\S+)\";`)
		secret := pattern.FindStringSubmatch(key[2])
		if len(secret) < 2 {
			log.Warnf("No key secret found for name %s", name)
			return nil
		}

		// this key clause matches the name we are looking for
		controlKey = &Bind9RndcKey{
			Name:      name,
			Algorithm: algorithm[1],
			Secret:    secret[1],
		}
		break
	}

	return controlKey
}

// parseInetSpec parses an inet statement from a named configuration excerpt.
// The inet statement is defined by inet_spec:
//
//	inet_spec = ( ip_addr | * ) [ port ip_port ]
//				allow { address_match_list }
//				keys { key_list };
//
// This function returns the ip_addr, port and the first key that is
// referenced in the key_list.  If instead of an ip_addr, the asterisk (*) is
// specified, this function will return 'localhost' as an address.
func parseInetSpec(config, excerpt string) (address string, port int64, key *Bind9RndcKey) {
	pattern := regexp.MustCompile(
		`(?s)` + // Enable multiline mode (\s matches a new line).
			`inet` + // Fixed prefix statement.
			`\s+` + // Mandatory spacing.
			// First matching group.
			`(\S+\s*\S*\s*\d*)` + // ( ip_addr | * ) [ port ip_port ]
			`\s+` + // Mandatory spacing.
			`allow` + // Fixed statement.
			`\s*\{\s*` + // Opening clause surrounded by optional spacing.
			// Non-matching group repeated zero or more times.
			`(?:\s*\S+\s*;\s*)*` + // address_match_list
			`\s*\}` + // Closing clause prepended by an optional spacing.
			// Second matching group.
			`(.*)` + // keys { key_list }; (pattern matched below)
			`;`, // Trailing semicolon.
	)
	match := pattern.FindStringSubmatch(excerpt)
	if len(match) == 0 {
		log.Warnf("Cannot parse BIND 9 inet configuration: no match (%+v)", config)
		return "", 0, nil
	}

	inetSpec := regexp.MustCompile(`\s+`).Split(match[1], 3)
	switch len(inetSpec) {
	case 1:
		address = inetSpec[0]
	case 3:
		address = inetSpec[0]
		if inetSpec[1] != "port" {
			log.Warnf("Cannot parse BIND 9 control port: bad port statement (%+v)", inetSpec)
			return "", 0, nil
		}

		iPort, err := strconv.Atoi(inetSpec[2])
		if err != nil {
			log.Warnf("Cannot parse BIND 9 control port: %+v (%+v)", inetSpec, err)
			return "", 0, nil
		}
		port = int64(iPort)
	case 2:
	default:
		log.Warnf("Cannot parse BIND 9 inet_spec configuration: no match (%+v)", inetSpec)
		return "", 0, nil
	}

	if len(match) == 3 {
		// Find a key clause. This pattern is build up like this:
		// keys\s*                - keys
		// \{\s*                  - {
		// \"(.*?)\"\s*;\s*       - key_list (first)
		// (?:\s*\".*?\"\s*;\s*)* - key_list (remainder)
		// \}                     - }
		pattern = regexp.MustCompile(`(?s)keys\s*\{\s*\"(.*?)\"\s*;\s*(?:\s*\".*?\"\s*;\s*)*\}`)

		keyName := pattern.FindStringSubmatch(match[2])
		if len(keyName) > 1 {
			key = getRndcKey(config, keyName[1])
			if key == nil {
				log.WithField("key", keyName[1]).Warn("Cannot find key details")
			}
		}
	}

	if address == "*" {
		address = "localhost"
	}

	return address, port, key
}

// getCtrlAddressFromBind9Config retrieves the rndc control access address,
// port, and secret key (if configured) from the configuration `text`.
//
// We need to cover the following cases:
// - no controls clause - BIND9 will open a control socket on localhost
// - empty controls clause - no control socket will be opened
// - controls clause with keys - BIND9 will open a control socket
//
// Multiple controls clauses may be configured but currently this function
// only matches the first one.  Multiple access points may be listed inside
// a single controls clause, but this function currently only matches the
// first in the list.  A controls clause may look like this:
//
//		controls {
//			inet 127.0.0.1 allow {localhost;};
//			inet * port 7766 allow {"rndc-users";};
//	        keys {"rndc-remote";};
//		};
//
// In this example, "rndc-users" and "rndc-remote" refer to an acl and key
// clauses.
//
// Finding the key is done by looking if the control access point has a
// keys parameter and if so, it looks in `path` for a key clause with the
// same name.
func getCtrlAddressFromBind9Config(text string) (controlAddress string, controlPort int64, controlKey *Bind9RndcKey) {
	// Match the following clause:
	//     controls {
	//         inet inet_spec [inet_spec] ;
	//     };
	// or
	//     controls { /maybe some whitespace chars here/ };
	pattern := regexp.MustCompile(`(?s)controls\s*\{\s*(.*)\s*\}\s*;`)
	controls := pattern.FindStringSubmatch(text)
	if len(controls) == 0 {
		log.Debugf("BIND9 has no `controls` clause, assuming defaults (127.0.0.1, port 953)")
		return "127.0.0.1", 953, nil
	}

	// See if there's any non-whitespace characters in the controls clause.
	// If not, there's `controls {};`, which means: disable control socket.
	txt := strings.TrimSpace(controls[1])
	if len(txt) == 0 {
		log.Debugf("BIND9 has rndc support disabled (empty 'controls' found)")
		return "", 0, nil
	}

	// We only pick the first match, but the controls clause
	// can list multiple control access points.
	controlAddress, controlPort, controlKey = parseInetSpec(text, controls[1])
	if controlAddress != "" {
		// If no port was provided, use the default rndc port.
		if controlPort == 0 {
			controlPort = RndcDefaultPort
		}
	}
	return controlAddress, controlPort, controlKey
}

// getStatisticsChannelFromBind9Config retrieves the statistics channel access
// address, port, and secret key (if configured) from the configuration `text`.
//
// Multiple statistics-channels clauses may be configured but currently this
// function only matches the first one.  Multiple access points may be listed
// inside a single controls clause, but this function currently only matches
// the first in the list.  A statistics-channels clause may look like this:
//
//	statistics-channels {
//		inet 10.1.10.10 port 8080 allow { 192.168.2.10; 10.1.10.2; };
//		inet 127.0.0.1  port 8080 allow { "stats-clients" };
//	};
//
// In this example, "stats-clients" refers to an acl clause.
func getStatisticsChannelFromBind9Config(text string) (statsAddress string, statsPort int64) {
	// Match the following clause:
	//     statistics-channels {
	//         inet inet_spec [inet_spec] ;
	//     };
	pattern := regexp.MustCompile(`(?s)statistics-channels\s*\{\s*(.*)\s*\}\s*;`)
	channels := pattern.FindStringSubmatch(text)
	if len(channels) == 0 {
		return "", 0
	}

	// We only pick the first match, but the statistics-channels clause
	// can list multiple control access points.
	statsAddress, statsPort, _ = parseInetSpec(text, channels[1])
	if statsAddress != "" {
		// If no port was provided, use the default statistics channel port.
		if statsPort == 0 {
			statsPort = StatsChannelDefaultPort
		}
	}
	return statsAddress, statsPort
}

// Determine executable using base named directory or system default paths.
func determineBinPath(baseNamedDir, executable string, executor storkutil.CommandExecutor) (string, error) {
	// look for executable in base named directory and sbin or bin subdirectory
	if baseNamedDir != "" {
		for _, binDir := range []string{"sbin", "bin"} {
			fullPath := path.Join(baseNamedDir, binDir, executable)
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, nil
			}
		}
	}

	// not found so try to find generally in the system
	fullPath, err := executor.LookPath(executable)
	if err != nil {
		return "", errors.Errorf("cannot determine location of %s", executable)
	}
	return fullPath, nil
}

// Get potential locations of named.conf.
func getPotentialNamedConfLocations() []string {
	return []string{
		"/etc/bind/named.conf",
		"/etc/opt/isc/isc-bind/named.conf",
		"/etc/opt/isc/scls/isc-bind/named.conf",
		"/usr/local/etc/namedb/named.conf",
	}
}

// Parses output of the named -V, which contains default paths for named, rndc
// configurations among many other things. Returns one string:
// default path for named configuration (may be empty if parsing fails).

// The output of named -V contains the following info we're looking for:
//
// default paths:
//
//	named configuration:  /etc/bind/named.conf
//	rndc configuration:   /etc/bind/rndc.conf
//	DNSSEC root key:      /etc/bind/bind.keys
//	nsupdate session key: //run/named/session.key
//	named PID file:       //run/named/named.pid
//	named lock file:      //run/named/named.lock
//	geoip-directory:      /usr/share/GeoIP
func parseNamedDefaultPath(output []byte) string {
	// Using []byte is inconvenient to use, let's convert to plain string first.
	text := string(output)

	// Let's use regexp to find interesting string.
	namedConfPattern := regexp.MustCompile(`named configuration: *(\/.*)`)
	namedConfMatch := namedConfPattern.FindStringSubmatch(text)

	// If found, Match is an array of strings. match[0] is always full string,
	// match[1] is the first (and only in this case) group.
	if len(namedConfMatch) < 2 {
		log.Warnf("Unable to find 'named configuration:' line in 'named -V' output.")
		return ""
	}

	return namedConfMatch[1]
}

// Detects the running Bind 9 application.
// It accepts the components of the Bind 9 process name (the "match" argument),
// the current working directory of the process (the "cwd" argument; it may be
// empty), and a command executor instance. It uses multiple steps to attempt
// detection:
//
// Step 1: Try to parse -c parameter of the running process.
// Step 2: Checks if STORK_BIND9_CONFIG is defined. If it is, uses that.
// Step 3: Try to parse output of the named -V command.
// Step 4: Try to find named.conf in the default locations.
//
// Returns the collected data or nil if the Bind 9 is not recognized or any
// error occurs.
func detectBind9App(match []string, cwd string, executor storkutil.CommandExecutor) App {
	if len(match) < 3 {
		log.Warnf("Problem with parsing BIND 9 cmdline: %s", match[0])
		return nil
	}

	// try to find bind9 config file(s)
	namedDir := match[1]
	bind9Params := match[2]
	bind9ConfPath := ""

	// look for config file in cmd params
	paramsPattern := regexp.MustCompile(`-c\s+(\S+)`)
	m := paramsPattern.FindStringSubmatch(bind9Params)

	// STEP 1: Let's try to parse -c parameter passed to named.
	log.Debug("Looking for BIND 9 config file in -c parameter of a running process.")
	if m != nil {
		bind9ConfPath = m[1]
		// if path to config is not absolute then join it with CWD of named
		if !path.IsAbs(bind9ConfPath) {
			bind9ConfPath = path.Join(cwd, bind9ConfPath)
		}
		log.Debugf("Found BIND 9 config file in %s based on -c parameter of a running process.", bind9ConfPath)
	}

	// STEP 2: Check if STORK_BIND9_CONFIG variable is specified it is, we'll use
	// whatever value is provided. User knows best *cough*.
	if bind9ConfPath == "" {
		if f, ok := os.LookupEnv("STORK_BIND9_CONFIG"); ok {
			log.Debugf("Looking for BIND 9 config in %s as specified in STORK_BIND9_CONFIG variable.", f)
			if _, err := os.Stat(f); err == nil {
				bind9ConfPath = f
				log.Infof("Found BIND 9 config file in %s, based on STORK_BIND9_CONFIG variable", f)
			} else {
				log.Errorf("File specified in STORK_BIND9_CONFIG (%s) not found or unreadable.", f)
			}
		}
	}

	// STEP 3: If we still don't have anything, let's try to run named -V and
	// parse its output.
	// determine config directory
	bind9ConfDir := path.Dir(bind9ConfPath)

	// determine base named directory
	baseNamedDir := ""
	if namedDir != "" {
		// remove sbin or bin at the end
		baseNamedDir, _ = filepath.Split(strings.TrimRight(namedDir, "/"))
	}

	if bind9ConfPath == "" {
		log.Debugf("Looking for BIND 9 config file in output of `named -V`.")
		namedPath, err := determineBinPath(baseNamedDir, namedExec, executor)
		if err != nil {
			log.Warnf("cannot determine BIND 9 executable %s: %s", namedExec, err)
			return nil
		}
		out, err := executor.Output(namedPath, "-V")
		if err != nil {
			log.Warnf("Attempt to run '%s -V' failed. I give up. Error: %s", namedPath, err)
			return nil
		}
		bind9ConfPath = parseNamedDefaultPath(out)
		if len(bind9ConfPath) > 0 {
			log.Infof("Found BIND 9 config file in %s based on output of `named -V`.", bind9ConfPath)
		}
	}

	// STEP 4: If we still don't have anything, let's look at typical locations.
	if bind9ConfPath == "" {
		log.Debugf("Looking for BIND 9 config file in typical locations.")
		// config path not found in cmdline params so try to guess its location
		for _, f := range getPotentialNamedConfLocations() {
			log.Debugf("Looking for BIND 9 config file in %s", f)
			if _, err := os.Stat(f); err == nil {
				bind9ConfPath = f
				log.Infof("Found BIND 9 config file in %s based on typical locations.", bind9ConfPath)
				break
			}
		}
	}

	// no config file so nothing to do
	if bind9ConfPath == "" {
		log.Warnf("Cannot find config file for BIND 9")
		return nil
	}

	// run named-checkconf on main config file and get preprocessed content of whole config
	namedCheckconfPath, err := determineBinPath(baseNamedDir, namedCheckconfExec, executor)
	if err != nil {
		log.Warnf("Cannot find BIND 9 %s: %s", namedCheckconfExec, err)
		return nil
	}

	out, err := executor.Output(namedCheckconfPath, "-p", bind9ConfPath)
	if err != nil {
		log.Warnf("Cannot parse BIND 9 config file %s: %+v; %s", bind9ConfPath, err, out)
		return nil
	}
	cfgText := string(out)

	// look for control address in config
	ctrlAddress, ctrlPort, ctrlKey := getCtrlAddressFromBind9Config(cfgText)
	if ctrlPort == 0 || len(ctrlAddress) == 0 {
		log.Warnf("Found BIND 9 config file (%s) but rndc support was disabled (empty `controls` clause)", bind9ConfPath)
		return nil
	}

	rndcKey := ""
	if ctrlKey != nil {
		rndcKey = ctrlKey.String()
	}

	accessPoints := []AccessPoint{
		{
			Type:    AccessPointControl,
			Address: ctrlAddress,
			Port:    ctrlPort,
			Key:     rndcKey,
		},
	}

	// look for statistics channel address in config
	address, port := getStatisticsChannelFromBind9Config(cfgText)
	if port > 0 && len(address) != 0 {
		accessPoints = append(accessPoints, AccessPoint{
			Type:    AccessPointStatistics,
			Address: address,
			Port:    port,
		})
	} else {
		log.Warnf("Cannot parse BIND 9 statistics-channels clause. Unable to gather statistics.")
	}

	// rndc is the command to interface with BIND 9.
	rndc := func(command []string) ([]byte, error) {
		cmd := exec.Command(command[0], command[1:]...) //nolint:gosec
		return cmd.Output()
	}

	// determine rndc details
	rndcClient := NewRndcClient(rndc)
	err = rndcClient.DetermineDetails(baseNamedDir, bind9ConfDir, ctrlAddress, ctrlPort, ctrlKey, executor)
	if err != nil {
		log.Warnf("Cannot determine BIND 9 rndc details: %s", err)
		return nil
	}

	// prepare final BIND 9 app
	bind9App := &Bind9App{
		BaseApp: BaseApp{
			Type:         AppTypeBind9,
			AccessPoints: accessPoints,
		},
		RndcClient: rndcClient,
	}

	return bind9App
}

// Send a command to named using rndc client.
func (ba *Bind9App) sendCommand(command []string) (output []byte, err error) {
	return ba.RndcClient.SendCommand(command)
}
