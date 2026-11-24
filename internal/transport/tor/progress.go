package tor

import (
	"fmt"
	"strconv"
	"strings"
)

type Phase uint8

const (
	StartingProcess Phase = iota
	StartingTransport
	ConnectingController
	Bootstrapping
	PublishingService
	Ready
)

type BootstrapStep uint8

const (
	BootstrapStarting BootstrapStep = iota
	BootstrapTransport
	BootstrapConnecting
	BootstrapHandshake
	BootstrapDirectory
	BootstrapCircuit
	BootstrapDone
)

type BootstrapProblem uint8

const (
	NoBootstrapProblem BootstrapProblem = iota
	BootstrapWarning
	BootstrapTimeout
	BootstrapRefused
	BootstrapUnreachable
	BootstrapTransportFailed
)

// Progress contains only bounded values. Tor SUMMARY/WARNING/HOST fields,
// raw replies, bridge addresses and identity material never leave the adapter.
type Progress struct {
	Phase   Phase
	Percent int
	Step    BootstrapStep
	Problem BootstrapProblem
}

type BootstrapError struct {
	Progress Progress
	Cause    error
}

func (e *BootstrapError) Error() string {
	if e.Progress.Phase == PublishingService {
		return fmt.Sprintf("Tor publish onion service: %v", e.Cause)
	}
	return fmt.Sprintf("Tor bootstrap (%d%%): %v", e.Progress.Percent, e.Cause)
}
func (e *BootstrapError) Unwrap() error { return e.Cause }

type progressReporter struct {
	callback func(Progress)
	last     Progress
	seen     bool
}

func (r *progressReporter) report(p Progress) {
	if r.seen && r.last == p {
		return
	}
	r.last, r.seen = p, true
	if r.callback != nil {
		r.callback(p)
	}
}

func parseBootstrap(raw string) (Progress, bool) {
	p := Progress{Phase: Bootstrapping}
	fields, ok := controlFields(raw)
	if !ok || len(fields) < 4 || (fields[0] != "NOTICE" && fields[0] != "WARN") || fields[1] != "BOOTSTRAP" {
		return p, false
	}
	values := make(map[string]string)
	for _, field := range fields[2:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return p, false
		}
		if _, duplicate := values[key]; duplicate {
			return p, false
		}
		values[key] = value
	}
	percent, err := strconv.Atoi(values["PROGRESS"])
	if err != nil || percent < 0 || percent > 100 {
		return p, false
	}
	p.Percent = percent
	switch values["TAG"] {
	case "conn_pt", "conn_done_pt", "conn_proxy", "conn_done_proxy":
		p.Step = BootstrapTransport
	case "conn", "conn_done", "conn_or", "conn_done_or":
		p.Step = BootstrapConnecting
	case "handshake", "handshake_done", "handshake_or", "handshake_done_or":
		p.Step = BootstrapHandshake
	case "onehop_create", "requesting_status", "loading_status", "loading_keys", "requesting_descriptors", "loading_descriptors", "enough_dirinfo":
		p.Step = BootstrapDirectory
	case "ap_conn", "ap_conn_done", "ap_handshake", "ap_handshake_done", "circuit_create":
		p.Step = BootstrapCircuit
	case "done":
		p.Step = BootstrapDone
	}
	if fields[0] == "WARN" {
		p.Problem = BootstrapWarning
		switch values["REASON"] {
		case "TIMEOUT":
			p.Problem = BootstrapTimeout
		case "CONNECTREFUSED":
			p.Problem = BootstrapRefused
		case "NOROUTE", "CONNECTRESET", "IOERROR":
			p.Problem = BootstrapUnreachable
		case "PT_MISSING":
			p.Problem = BootstrapTransportFailed
		}
	}
	return p, true
}

// Quoted summaries can contain text resembling PROGRESS or REASON. Split only
// outside quotes; never interpret a word inside a summary as a status field.
func controlFields(raw string) ([]string, bool) {
	if len(raw) > 16384 || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, false
	}
	var fields []string
	start, quoted, escaped := -1, false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if start < 0 {
			if c == ' ' || c == '\t' {
				continue
			}
			start = i
		}
		if escaped {
			escaped = false
		} else if quoted && c == '\\' {
			escaped = true
		} else if c == '"' {
			quoted = !quoted
		} else if !quoted && (c == ' ' || c == '\t') {
			fields = append(fields, raw[start:i])
			start = -1
			if len(fields) > 64 {
				return nil, false
			}
		}
	}
	if quoted || escaped {
		return nil, false
	}
	if start >= 0 {
		fields = append(fields, raw[start:])
	}
	return fields, true
}
