// Package smsviewer ports V1's dashboard SMS panel - reads the same
// `content query --uri content://sms` output V1's awk-based parser
// scraped, but in Go rather than shell/awk (which needed RS="Row: "
// busybox-awk-specific tricks and manual last-index scanning to handle
// a message body that might itself contain the literal text ", date=").
// Read-only - this package never sends or modifies messages, only
// reads what the forwarder app (a separate, already-existing piece)
// also reads.
package smsviewer

import (
	"os/exec"
	"strconv"
	"strings"
)

type Message struct {
	Address string
	Body    string
	DateMs  int64
}

const rowPrefix = "Row: "

// Parse splits raw `content query` output into messages, newest-first
// (matching the query's own "date DESC" sort), capped at limit.
// Exported and string-based so it's unit-testable against captured
// real output without a device. Uses the LAST ", date=" occurrence in
// each row (not the first) in case a message body coincidentally
// contains that exact substring - same defensive choice V1's awk
// parser made.
func Parse(output string, limit int) []Message {
	var msgs []Message
	for _, block := range splitRows(output) {
		if len(msgs) >= limit {
			break
		}
		msg, ok := parseRow(block)
		if ok {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

func splitRows(output string) []string {
	var blocks []string
	rest := output
	for {
		idx := strings.Index(rest, rowPrefix)
		if idx == -1 {
			break
		}
		rest = rest[idx+len(rowPrefix):]
		// Skip the row number itself (e.g. "0 address=...").
		if sp := strings.IndexByte(rest, ' '); sp != -1 {
			rest = rest[sp+1:]
		}
		next := strings.Index(rest, rowPrefix)
		if next == -1 {
			blocks = append(blocks, rest)
			break
		}
		blocks = append(blocks, rest[:next])
		rest = rest[next:]
	}
	return blocks
}

func parseRow(block string) (Message, bool) {
	const addrPrefix = "address="
	const bodyMarker = ", body="
	const dateMarker = ", date="

	aStart := strings.Index(block, addrPrefix)
	if aStart == -1 {
		return Message{}, false
	}
	aStart += len(addrPrefix)
	bRel := strings.Index(block[aStart:], bodyMarker)
	if bRel == -1 {
		return Message{}, false
	}
	address := block[aStart : aStart+bRel]
	bodyStart := aStart + bRel + len(bodyMarker)

	dRel := strings.LastIndex(block[bodyStart:], dateMarker)
	if dRel == -1 {
		return Message{}, false
	}
	body := block[bodyStart : bodyStart+dRel]
	dateStr := strings.TrimRight(block[bodyStart+dRel+len(dateMarker):], "\r\n \t")

	dateMs, err := strconv.ParseInt(dateStr, 10, 64)
	if err != nil {
		return Message{}, false
	}
	return Message{Address: address, Body: body, DateMs: dateMs}, true
}

// Fetch runs the real content query and parses its output, capped at
// limit messages.
func Fetch(limit int) ([]Message, error) {
	out, err := exec.Command("content", "query",
		"--uri", "content://sms",
		"--projection", "address:body:date",
		"--where", "type=1",
		"--sort", "date DESC",
	).CombinedOutput()
	if err != nil {
		return nil, err
	}
	return Parse(string(out), limit), nil
}
