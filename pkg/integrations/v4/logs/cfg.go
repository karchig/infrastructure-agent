// Copyright 2020 New Relic Corporation. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
package logs

import (
	"bytes"
	"fmt"
	"github.com/newrelic/infrastructure-agent/pkg/config"
	"github.com/newrelic/infrastructure-agent/pkg/license"
	"github.com/newrelic/infrastructure-agent/pkg/log"
	"github.com/pkg/errors"
	"path/filepath"
	"regexp"
	"simonwaldherr.de/go/ranger"
	"strconv"
	"strings"
	"text/template"
)

var cfgLogger = log.WithComponent("integrations.Supervisor.Config").WithField("process", "log-forwarder")

// FluentBit default values.
const (
	euEndpoint              = "https://log-api.eu.newrelic.com/log/v1"
	stagingEndpoint         = "https://staging-log-api.newrelic.com/log/v1"
	logRecordModifierSource = "nri-agent"
	defaultBufferMaxSize    = 128
	fluentBitDbName         = "fb.db"
)

// FluentBit INPUT plugin types
const (
	fbInputTypeTail    = "tail"
	fbInputTypeSystemd = "systemd"
	fbInputTypeWinlog  = "winlog"
	fbInputTypeSyslog  = "syslog"
	fbInputTypeTcp     = "tcp"
)

// FluentBit FILTER plugin types
const (
	fbFilterTypeGrep           = "grep"
	fbFilterTypeRecordModifier = "record_modifier"
)

// Syslog plugin valid formats
const (
	syslogRegex     = `^(tcp|udp|unix_tcp|unix_udp)://.*`
	tcpRegex        = `^tcp://(\d{1,3}\.){3}\d{1,3}:\d+`
	tcpUdpRegex     = `^(tcp|udp)://(\d{1,3}\.){3}\d{1,3}:\d+`
	unixSocketRegex = `^unix_(udp|tcp):///.*`
)

// Winlog constants
const (
	eventIdRangeRegex = `^(\d+-\d+)$`
)

const (
	rAttEntityGUID = "entity.guid.INFRA"
	rAttFbInput    = "fb.input"
	rAttPluginType = "plugin.type"
	rAttHostname   = "hostname"
)

const (
	fbGrepFieldForTail          = "log"
	fbGrepFieldForSystemd       = "MESSAGE"
	fbGrepFieldForSyslog        = "message"
	fbGrepFieldForTcpPlain      = "log"
	fbGrepFieldForWinlogEventId = "EventID"
)

// LogsCfg stores logging product configuration split by block entries.
type LogsCfg []LogCfg

// YAML yaml format logs config file.
type YAML struct {
	Logs LogsCfg `yaml:"logs"`
}

// LogCfg logging integration config from customer defined YAML.
type LogCfg struct {
	Name       string            `yaml:"name"`
	File       string            `yaml:"file"`        // ...
	MaxLineKb  int               `yaml:"max_line_kb"` // Setup the max value of the buffer while reading lines.
	Folder     string            `yaml:"folder"`      // ...
	Systemd    string            `yaml:"systemd"`     // ...
	EventLog   string            `yaml:"eventlog"`
	Pattern    string            `yaml:"pattern"`
	Attributes map[string]string `yaml:"attributes"`
	Syslog     *LogSyslogCfg     `yaml:"syslog"`
	Tcp        *LogTcpCfg        `yaml:"tcp"`
	Fluentbit  *LogExternalFBCfg `yaml:"fluentbit"`
	Winlog     *LogWinlogCfg     `yaml:"winlog"`
}

// LogSyslogCfg logging integration config from customer defined YAML, specific for the Syslog input plugin
type LogSyslogCfg struct {
	URI             string `yaml:"uri"`
	Parser          string `yaml:"parser"`
	UnixPermissions string `yaml:"unix_permissions"`
}

type LogWinlogCfg struct {
	Channel         string   `yaml:"channel"`
	CollectEventIds []string `yaml:"collect-eventids"`
	ExcludeEventIds []string `yaml:"exclude-eventids"`
}

type LogTcpCfg struct {
	Uri       string `yaml:"uri"`
	Format    string `yaml:"format"`
	Separator string `yaml:"separator"`
}

type LogExternalFBCfg struct {
	CfgPath     string `yaml:"config_file"`
	ParsersPath string `yaml:"parsers_file"`
}

// IsValid validates struct as there's no constructor to enforce it.
func (l *LogCfg) IsValid() bool {
	return l.Name != "" && (l.File != "" || l.Folder != "" || l.Systemd != "" || l.EventLog != "" || l.Syslog != nil || l.Tcp != nil || l.Fluentbit != nil || l.Winlog !=nil)
}

// FBCfg FluentBit automatically generated configuration.
type FBCfg struct {
	Inputs      []FBCfgInput
	Parsers     []FBCfgParser
	ExternalCfg FBCfgExternal
	Output      FBCfgOutput
}

// Format will return the FBCfg in the fluent bit config file format.
func (c FBCfg) Format() (result string, externalCfg FBCfgExternal, err error) {
	buf := new(bytes.Buffer)
	tpl, err := template.New("fb cfg").Parse(fbConfigFormat)
	if err != nil {
		return "", FBCfgExternal{}, errors.Wrap(err, "cannot parse log-forwarder template")
	}
	err = tpl.Execute(buf, c)
	if err != nil {
		return "", FBCfgExternal{}, errors.Wrap(err, "cannot write log-forwarder template")
	}

	return buf.String(), c.ExternalCfg, nil
}

// FBCfgInput FluentBit Input config block for either "tail", "systemd", "winlog" or "syslog" plugins.
// Tail plugin expected shape:
//  [INPUT]
//    Name tail
//    Path /var/log/newrelic-infra/newrelic-infra.log
//    Tag  nri-file
//    DB   fb.db
// Systemd plugin expected shape:
// [INPUT]
//   Name           systemd
//   Systemd_Filter _SYSTEMD_UNIT=newrelic-infra.service
//   Tag            newrelic-infra
//   DB             fb.db
type FBCfgInput struct {
	Name                  string
	Tag                   string
	DB                    string
	Path                  string // plugin: tail
	BufferMaxSize         string // plugin: tail
	SkipLongLines         string // always on
	Systemd_Filter        string // plugin: systemd
	Channels              string // plugin: winlog
	SyslogMode            string // plugin: syslog
	SyslogListen          string // plugin: syslog
	SyslogPort            int    // plugin: syslog
	SyslogParser          string // plugin: syslog
	SyslogUnixPath        string // plugin: syslog
	SyslogUnixPermissions string // plugin: syslog
	BufferChunkSize       string // plugin: syslog udp/udp_unix
	TcpListen             string // plugin: tcp
	TcpPort               int    // plugin: tcp
	TcpFormat             string // plugin: tcp
	TcpSeparator          string // plugin: tcp
	TcpBufferSize         int    // plugin: tcp (note that the "tcp" plugin uses Buffer_Size (without "k"s!) instead of Buffer_Max_Size (with "k"s!))
}

// FBCfgParser FluentBit Parser config block, only "grep" and "record_modifier" plugin supported.
//  [FILTER]
//    Name   grep
//    Match  nri-service
//    Regex  MESSAGE info
type FBCfgParser struct {
	Name         string
	Match        string
	RegexInclude string // plugin: grep
	RegexExclude string
	Records      map[string]string // plugin: record_modifier
}

// FBCfgOutput FluentBit Output config block, supporting NR output plugin.
// https://github.com/newrelic/newrelic-fluent-bit-output
type FBCfgOutput struct {
	Name              string
	Match             string
	LicenseKey        string
	Endpoint          string // empty for US, value required for EU or staging
	IgnoreSystemProxy bool
	Proxy             string
	CABundleFile      string
	CABundleDir       string
	ValidateCerts     bool
}

// FBCfgExternal represents an existing set of native FluentBit configuration files
// that should be merged with the auto-generated FB configuration
type FBCfgExternal struct {
	CfgFilePath     string
	ParsersFilePath string
}

// NewFBConf creates a FluentBit config from several logging integration configs.
func NewFBConf(loggingCfgs LogsCfg, logFwdCfg *config.LogForward, entityGUID, hostname string) (fb FBCfg, e error) {
	fb = FBCfg{
		Inputs:  []FBCfgInput{},
		Parsers: []FBCfgParser{},
	}

	for _, block := range loggingCfgs {
		input, filters, external, err := parseConfigBlock(block, logFwdCfg.HomeDir)
		if err != nil {
			return
		}

		if (input != FBCfgInput{}) {
			fb.Inputs = append(fb.Inputs, input)
		}

		fb.Parsers = append(fb.Parsers, filters...)

		if (external != FBCfgExternal{} && fb.ExternalCfg != FBCfgExternal{}) {
			cfgLogger.Warn("External Fluent Bit configuration specified more than once. Only first one is considered, please remove any duplicates from the configuration.")
		} else if (external != FBCfgExternal{}) {
			fb.ExternalCfg = external
		}
	}

	if (len(fb.Inputs) == 0 && fb.ExternalCfg == FBCfgExternal{}) {
		return
	}

	// This record_modifier FILTER adds common attributes for all the log records
	fb.Parsers = append(fb.Parsers, FBCfgParser{
		Name:  fbFilterTypeRecordModifier,
		Match: "*",
		Records: map[string]string{
			rAttEntityGUID: entityGUID,
			rAttPluginType: logRecordModifierSource,
			rAttHostname:   hostname,
		},
	})

	// Newrelic OUTPUT plugin will send all the collected logs to Vortex
	fb.Output = newNROutput(logFwdCfg)

	return
}

func parseConfigBlock(l LogCfg, logsHomeDir string) (input FBCfgInput, filters []FBCfgParser, external FBCfgExternal, err error) {
	if l.Fluentbit != nil {
		external = newFBExternalConfig(*l.Fluentbit)
		return
	}

	dbPath := filepath.Join(logsHomeDir, fluentBitDbName)

	if l.File != "" {
		input, filters = parseFileInput(l, dbPath)
	} else if l.Folder != "" {
		input, filters = parseFolderInput(l, dbPath)
	} else if l.Systemd != "" {
		input, filters = parseSystemdInput(l, dbPath)
	} else if l.EventLog != "" {
		input, filters = parseEventLogInput(l, dbPath)
	} else if l.Syslog != nil {
		input, filters, err = parseSyslogInput(l)
	} else if l.Tcp != nil {
		input, filters, err = parseTcpInput(l)
	} else if l.Winlog != nil {
		input, filters, err = parseWinlogInput(l, dbPath)
	}

	if err != nil {
		return
	}

	if (input == FBCfgInput{}) {
		err = fmt.Errorf("invalid log integration config")
		return
	} else {
		return input, filters, FBCfgExternal{}, nil
	}
}

// Single file
func parseFileInput(l LogCfg, dbPath string) (input FBCfgInput, filters []FBCfgParser) {
	input = newFileInput(l.File, dbPath, l.Name, getBufferMaxSize(l))
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeTail, l.Attributes))
	filters = parsePattern(l, fbGrepFieldForTail, filters)
	return input, filters
}

// Multiple files: expands folder into several "tail" plugin inputs
func parseFolderInput(l LogCfg, dbPath string) (input FBCfgInput, filters []FBCfgParser) {
	// /path/to/folder results in /path/to/folder/*
	folderPath := filepath.Join(l.Folder, "*")
	input = newFileInput(folderPath, dbPath, l.Name, getBufferMaxSize(l))
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeTail, l.Attributes))
	filters = parsePattern(l, fbGrepFieldForTail, filters)
	return input, filters
}

// Systemd service: "system" plugin input
func parseSystemdInput(l LogCfg, dbPath string) (input FBCfgInput, filters []FBCfgParser) {
	input = newSystemdInput(l.Systemd, dbPath, l.Name)
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeSystemd, l.Attributes))
	filters = parsePattern(l, fbGrepFieldForSystemd, filters)
	return input, filters
}

// Windows eventlog: "winlog" plugin
func parseEventLogInput(l LogCfg, dbPath string) (input FBCfgInput, filters []FBCfgParser) {
	input = newWindowsEventlogInput(l.EventLog, dbPath, l.Name)
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeWinlog, l.Attributes))
	return input, filters
}

// Syslog: "syslog" plugin
func parseSyslogInput(l LogCfg) (input FBCfgInput, filters []FBCfgParser, err error) {
	slIn, e := newSyslogInput(*l.Syslog, l.Name, getBufferMaxSize(l))
	if e != nil {
		return FBCfgInput{}, nil, e
	}
	input = slIn
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeSyslog, l.Attributes))
	filters = parsePattern(l, fbGrepFieldForSyslog, filters)
	return input, filters, nil
}

// Tcp: "tcp plugin
func parseTcpInput(l LogCfg) (input FBCfgInput, filters []FBCfgParser, err error) {
	tcpIn, e := newTcpInput(*l.Tcp, l.Name, getBufferMaxSize(l))
	if e != nil {
		err = e
		return
	}
	input = tcpIn
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeTcp, l.Attributes))
	if l.Tcp.Format == "none" {
		filters = parsePattern(l, fbGrepFieldForTcpPlain, filters)
	}
	return input, filters, nil
}

//Winlog: "winlog" plugin
func parseWinlogInput(l LogCfg, dbPath string) (input FBCfgInput, filters []FBCfgParser, err error) {
	input = newWinlogInput(*l.Winlog, dbPath, l.Name)
	filters = append(filters, newRecordModifierFilterForInput(l.Name, fbInputTypeWinlog, l.Attributes))
	if included, excluded := l.Winlog.CollectEventIds, l.Winlog.ExcludeEventIds; len(included) > 0 || len(excluded) > 0 {
		var regexInclude string
		regexInclude, err = numberRangesToRegex(included)
		if err != nil {
			return
		}
		var regexExclude string
		regexExclude, err = numberRangesToRegex(excluded)
		if err != nil {
			return
		}
		eventIdGrep := newGrepFilter(l.Name, fbGrepFieldForWinlogEventId,regexInclude, regexExclude)
		filters = append(filters, eventIdGrep)
	}
	return input, filters, nil
}

func numberRangesToRegex(numberRanges []string) (regex string, e error) {
	var regexList []string
	for _, numberRange := range numberRanges {
		if match, err := regexp.MatchString(eventIdRangeRegex, numberRange); match && err == nil {
			var splitRange = strings.Split(numberRange, "-")
			bottomLimit, _ := strconv.Atoi(splitRange[0])
			topLimit, _ := strconv.Atoi(splitRange[1])
			regexList = append(regexList, fmt.Sprintf("^(%s)$",
				strings.Replace(ranger.Compile(bottomLimit, topLimit), `\\`, `\`, -1)))
		} else if _, err := strconv.Atoi(numberRange); err == nil {
			regexList = append(regexList, fmt.Sprintf("^%s$", numberRange))
		} else {
			return "", fmt.Errorf("invalid EventId or range format")
		}
	}
	return strings.Join(regexList, "|"), nil
}

func parsePattern(l LogCfg, fluentBitGrepField string, filters []FBCfgParser) []FBCfgParser {
	if l.Pattern != "" {
		return append(filters, newGrepFilter(l.Name, fluentBitGrepField, l.Pattern, ""))
	}
	return filters
}

func newFBExternalConfig(l LogExternalFBCfg) FBCfgExternal {
	return FBCfgExternal{
		CfgFilePath:     l.CfgPath,
		ParsersFilePath: l.ParsersPath,
	}
}

func newFileInput(filePath string, dbPath string, tag string, bufSize int) FBCfgInput {
	return FBCfgInput{
		Name:          fbInputTypeTail,
		Path:          filePath,
		DB:            dbPath,
		Tag:           tag,
		BufferMaxSize: fmt.Sprintf("%dk", bufSize),
		SkipLongLines: "On",
	}
}

func newSystemdInput(service string, dbPath string, tag string) FBCfgInput {
	return FBCfgInput{
		Name:           fbInputTypeSystemd,
		Systemd_Filter: fmt.Sprintf("_SYSTEMD_UNIT=%s.service", service),
		Tag:            tag,
		DB:             dbPath,
	}
}

func newWindowsEventlogInput(eventLog string, dbPath string, tag string) FBCfgInput {
	return FBCfgInput{
		Name:     fbInputTypeWinlog,
		Channels: eventLog,
		Tag:      tag,
		DB:       dbPath,
	}
}

func newWinlogInput(winlog LogWinlogCfg, dbPath string, tag string) FBCfgInput {
	return FBCfgInput{
		Name:     fbInputTypeWinlog,
		Channels: winlog.Channel,
		Tag:      tag,
		DB:       dbPath,
	}
}

func newSyslogInput(l LogSyslogCfg, tag string, bufSize int) (FBCfgInput, error) {

	if match, _ := regexp.MatchString(syslogRegex, l.URI); !match {
		return FBCfgInput{}, fmt.Errorf("syslog: wrong uri format or unsupported protocol (tcp, udp, unix_tcp, unix_udp) %s", l.URI)
	}

	protocolPath := strings.Split(l.URI, "://")
	protocol := protocolPath[0]

	isTcpUdp, _ := regexp.MatchString(tcpUdpRegex, l.URI)
	isUnixSocket, _ := regexp.MatchString(unixSocketRegex, l.URI)

	if (protocol == "udp" || protocol == "tcp") && !isTcpUdp ||
		(protocol == "unix_udp" || protocol == "unix_tcp") && !isUnixSocket {
		return FBCfgInput{}, fmt.Errorf("syslog: wrong uri format for %s %s", protocol, l.URI)
	}

	fbInput := FBCfgInput{
		Name:         fbInputTypeSyslog,
		Tag:          tag,
		SyslogMode:   protocol,
		SyslogParser: getSyslogParser(l.Parser),
	}

	if protocol == "tcp" || protocol == "udp" {
		listenPort := strings.Split(protocolPath[1], ":")
		fbInput.SyslogListen = listenPort[0]
		fbInput.SyslogPort, _ = strconv.Atoi(listenPort[1])
	} else {
		fbInput.SyslogUnixPath = protocolPath[1]
		fbInput.SyslogUnixPermissions = l.UnixPermissions
	}

	if protocol == "udp" || protocol == "unix_udp" {
		fbInput.BufferChunkSize = fmt.Sprintf("%dk", bufSize)
	} else {
		fbInput.BufferMaxSize = fmt.Sprintf("%dk", bufSize)
	}

	return fbInput, nil
}

func newTcpInput(t LogTcpCfg, tag string, bufSize int) (FBCfgInput, error) {
	if match, _ := regexp.MatchString(tcpRegex, t.Uri); !match {
		return FBCfgInput{}, fmt.Errorf("tcp: wrong uri format %s", t.Uri)
	}

	listenPort := strings.Split(t.Uri[6:], ":")
	port, _ := strconv.Atoi(listenPort[1])

	fbInput := FBCfgInput{
		Name:          fbInputTypeTcp,
		Tag:           tag,
		TcpListen:     listenPort[0],
		TcpPort:       port,
		TcpFormat:     t.Format,
		TcpBufferSize: bufSize,
	}

	if t.Format == "none" {
		fbInput.TcpSeparator = strings.Replace(t.Separator, `\\`, `\`, -1)
	}

	return fbInput, nil
}

func newRecordModifierFilterForInput(tag string, fbFilterInputType string, userAttributes map[string]string) FBCfgParser {
	ret := FBCfgParser{
		Name:  fbFilterTypeRecordModifier,
		Match: tag,
		Records: map[string]string{
			rAttFbInput: fbFilterInputType,
		},
	}

	for key, value := range userAttributes {
		if !isReserved(key) {
			ret.Records[key] = value
		} else {
			cfgLogger.WithField("attribute", key).Warn("attribute name is a reserved keyword and will be ignored, please use a different name")
		}
	}

	return ret
}

func newGrepFilter(tag, grepField, regexInclude, regexExclude string) FBCfgParser {
	grepFilter := FBCfgParser{
		Name:  fbFilterTypeGrep,
		Match: tag,
	}
	if len(regexInclude) > 0 {
		grepFilter.RegexInclude = fmt.Sprintf("%s %s", grepField, regexInclude)
	}

	if len(regexExclude) > 0 {
		grepFilter.RegexExclude = fmt.Sprintf("%s %s", grepField, regexExclude)
	}

	return grepFilter
}

func newNROutput(cfg *config.LogForward) FBCfgOutput {
	ret := FBCfgOutput{
		Name:              "newrelic",
		Match:             "*",
		LicenseKey:        cfg.License,
		IgnoreSystemProxy: cfg.ProxyCfg.IgnoreSystemProxy,
		Proxy:             cfg.ProxyCfg.Proxy,
		CABundleFile:      cfg.ProxyCfg.CABundleFile,
		CABundleDir:       cfg.ProxyCfg.CABundleDir,
		ValidateCerts:     cfg.ProxyCfg.ValidateCerts,
	}

	if cfg.IsStaging {
		ret.Endpoint = stagingEndpoint
	}

	if license.IsRegionEU(cfg.License) {
		ret.Endpoint = euEndpoint
	}

	return ret
}

func getBufferMaxSize(l LogCfg) int {
	bufferSize := l.MaxLineKb
	if bufferSize == 0 {
		bufferSize = defaultBufferMaxSize
	}

	return bufferSize
}

func isReserved(att string) bool {
	return att == rAttEntityGUID || att == rAttFbInput || att == rAttPluginType || att == rAttHostname
}

func getSyslogParser(p string) string {
	if p == "" {
		return "rfc3164"
	}
	return p
}
