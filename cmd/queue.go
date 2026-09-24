package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/spf13/cobra"
)

const queueDocs = "https://docs.koolbase.com/functions/queues"

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Run a function later, with retries",
	Long: "Queue a job for a function in this project: it runs in the background, optionally after a delay,\n" +
		"with the same retries and dead-letter queue as triggers. Owners and admins only.\n\n" +
		"From inside a function, use ctx.queue.enqueue instead.\nDocs: " + queueDocs,
}

// parsePayload accepts a JSON object; empty means {}.
func parsePayload(raw string) (map[string]interface{}, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}, nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("--payload is not valid JSON: %v", err)
	}
	obj, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("--payload must be a JSON object, like '{\"userId\":\"u_123\"}'")
	}
	return obj, nil
}

func formatQueueJobs(list []api.QueueJob) string {
	if len(list) == 0 {
		return "No pending jobs.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-38s %-24s %-8s %-26s %s\n", "JOB", "FUNCTION", "ATTEMPT", "RUNS AT", "LAST ERROR")
	for _, j := range list {
		last := ""
		if j.LastError != nil {
			last = *j.LastError
		}
		fmt.Fprintf(&b, "%-38s %-24s %-8s %-26s %s\n", j.ID, j.Function, fmt.Sprintf("%d/%d", j.Attempt, j.MaxAttempts), j.RunAt, last)
	}
	return b.String()
}

var queueEnqueueCmd = &cobra.Command{
	Use:     "enqueue <function>",
	Short:   "Queue a job for a function",
	Args:    cobra.ExactArgs(1),
	Example: `  koolbase queue enqueue send-welcome-email --payload '{"userId":"u_123"}' --delay 60`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		raw, _ := cmd.Flags().GetString("payload")
		payload, err := parsePayload(raw)
		if err != nil {
			return err
		}
		delay, _ := cmd.Flags().GetInt("delay")
		job, err := client.EnqueueJob(projectID, args[0], payload, delay)
		if err != nil {
			return err
		}
		fmt.Printf("Queued job %s for %s (runs at %s)\n", job.ID, job.Function, job.RunAt)
		return nil
	},
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending jobs, soonest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		jobs, err := client.ListQueueJobs(projectID)
		if err != nil {
			return err
		}
		fmt.Print(formatQueueJobs(jobs))
		return nil
	},
}

var queueCancelCmd = &cobra.Command{
	Use:   "cancel <job-id>",
	Short: "Cancel a job before it runs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		if err := client.CancelQueueJob(projectID, args[0]); err != nil {
			return err
		}
		fmt.Printf("Job %s cancelled\n", args[0])
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{queueEnqueueCmd, queueListCmd, queueCancelCmd} {
		c.Flags().StringP("project", "p", "", "Project ID")
		queueCmd.AddCommand(c)
	}
	queueEnqueueCmd.Flags().String("payload", "", "JSON object passed to the function as ctx.request.payload")
	queueEnqueueCmd.Flags().Int("delay", 0, "Seconds to wait before running (0 to 604800)")
	rootCmd.AddCommand(queueCmd)
}
