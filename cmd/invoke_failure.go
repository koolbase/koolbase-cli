package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

// formatInvokeFailure explains a failed invocation. The request reached the
// function and the function itself failed, so say that, then show which
// ctx.db call failed (the server's error field holds only the bare message,
// which on its own reads like the CLI's own request was rejected), and the
// log ID to look it up.
func formatInvokeFailure(resp *api.InvokeResponse) string {
	var b strings.Builder
	b.WriteString(" Function failed")
	if resp.Status != 0 {
		fmt.Fprintf(&b, " (status %d)", resp.Status)
	}
	b.WriteString("\n")

	var d struct {
		Type       string `json:"type"`
		Status     int    `json:"status"`
		Operation  string `json:"operation"`
		Collection string `json:"collection"`
		RecordID   string `json:"record_id"`
		Code       string `json:"code"`
	}
	parsed := len(resp.ErrorStructured) > 0 && json.Unmarshal(resp.ErrorStructured, &d) == nil
	switch {
	case parsed && d.Operation != "":
		fmt.Fprintf(&b, " Error: ctx.db.%s failed with HTTP %d: %s\n", d.Operation, d.Status, resp.Error)
		var details []string
		if d.Collection != "" {
			details = append(details, "collection: "+d.Collection)
		}
		if d.RecordID != "" {
			details = append(details, "record: "+d.RecordID)
		}
		if d.Code != "" {
			details = append(details, "code: "+d.Code)
		}
		if len(details) > 0 {
			fmt.Fprintf(&b, "   %s\n", strings.Join(details, "   "))
		}
	case parsed && d.Type != "" && d.Type != "Error":
		fmt.Fprintf(&b, " Error (%s): %s\n", d.Type, resp.Error)
	default:
		fmt.Fprintf(&b, " Error: %s\n", resp.Error)
	}
	if resp.LogID != "" {
		fmt.Fprintf(&b, "\n Log ID: %s\n", resp.LogID)
	}
	return b.String()
}
