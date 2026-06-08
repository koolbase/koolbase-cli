package cmd

import (
	"fmt"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var vectorFieldsCmd = &cobra.Command{
	Use:     "vector-fields",
	Aliases: []string{"vf"},
	Short:   "Manage vector fields on a collection",
}

var vfListCmd = &cobra.Command{
	Use:     "list <collection>",
	Short:   "List vector fields declared on a collection",
	Example: `  koolbase vector-fields list articles --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		fields, err := client.ListVectorFields(projectID, collection)
		if err != nil {
			return err
		}

		if len(fields) == 0 {
			fmt.Printf("No vector fields on %q.\n", collection)
			fmt.Printf("Declare one with: koolbase vector-fields create %s content_embedding --dimensions 1536 --project <id>\n", collection)
			return nil
		}

		fmt.Printf("%-24s %-6s %-10s %s\n", "NAME", "DIM", "DISTANCE", "AUTO-EMBED")
		fmt.Printf("%-24s %-6s %-10s %s\n", "----", "---", "--------", "----------")
		for _, f := range fields {
			ae := "—"
			if f.EmbeddingProvider != nil && f.SourceField != nil {
				model := "?"
				if f.EmbeddingModel != nil {
					model = *f.EmbeddingModel
				}
				ae = fmt.Sprintf("%s → %s (%s)", *f.EmbeddingProvider, *f.SourceField, model)
			}
			fmt.Printf("%-24s %-6d %-10s %s\n", f.FieldName, f.Dimensions, f.DistanceMetric, ae)
		}
		return nil
	},
}

var vfCreateCmd = &cobra.Command{
	Use:   "create <collection> <name>",
	Short: "Declare a vector field on a collection",
	Long: `Declare a vector field with a fixed dimensionality. Optionally configure
auto-embedding: the worker will embed a chosen source field via your
configured provider whenever a record is inserted or updated.

Valid dimensions: 384, 768, 1024, 1536.

To enable auto-embed, pass all three: --provider, --model, --source-field.
The provider must already be configured in this project with status=valid
(set one up in the dashboard under AI > Providers).`,
	Example: `  # Plain vector field (no auto-embed):
  koolbase vector-fields create articles content_embedding --dimensions 1536 --project proj_123

  # With auto-embedding via Gemini:
  koolbase vector-fields create articles content_embedding \
    --dimensions 1536 \
    --provider gemini \
    --model gemini-embedding-001 \
    --source-field content \
    --project proj_123`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		name := args[1]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		dimensions, _ := cmd.Flags().GetInt("dimensions")
		if dimensions == 0 {
			return fmt.Errorf("--dimensions is required (one of 384, 768, 1024, 1536)")
		}
		distance, _ := cmd.Flags().GetString("distance")
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		sourceField, _ := cmd.Flags().GetString("source-field")

		anySet := provider != "" || model != "" || sourceField != ""
		allSet := provider != "" && model != "" && sourceField != ""
		if anySet && !allSet {
			return fmt.Errorf("--provider, --model and --source-field must all be set together (or pass none for a plain vector field)")
		}

		req := api.CreateVectorFieldRequest{
			FieldName:      name,
			Dimensions:     dimensions,
			DistanceMetric: distance,
		}
		if allSet {
			req.EmbeddingProvider = &provider
			req.EmbeddingModel = &model
			req.SourceField = &sourceField
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		f, err := client.CreateVectorField(projectID, collection, req)
		if err != nil {
			return err
		}

		fmt.Printf("\nVector field declared\n")
		fmt.Printf("   Name:        %s\n", f.FieldName)
		fmt.Printf("   Collection:  %s\n", collection)
		fmt.Printf("   Dimensions:  %d\n", f.Dimensions)
		fmt.Printf("   Distance:    %s\n", f.DistanceMetric)
		if f.EmbeddingProvider != nil && f.SourceField != nil {
			mn := "—"
			if f.EmbeddingModel != nil {
				mn = *f.EmbeddingModel
			}
			fmt.Printf("   Auto-embed:  %s → %s (model: %s)\n", *f.EmbeddingProvider, *f.SourceField, mn)
		}
		return nil
	},
}

var vfUpdateCmd = &cobra.Command{
	Use:   "update <collection> <name>",
	Short: "Update or clear the auto-embed config on a vector field",
	Long: `Update auto-embedding on an existing vector field.

To configure auto-embed: pass all three of --provider, --model, --source-field.
To remove auto-embed entirely: pass --clear (mutually exclusive with the others).

You can't change a field's dimensions or distance metric — recreate it for that.`,
	Example: `  # Switch to OpenAI:
  koolbase vector-fields update articles content_embedding \
    --provider openai --model text-embedding-3-small --source-field content --project proj_123

  # Stop auto-embedding (existing vectors are kept):
  koolbase vector-fields update articles content_embedding --clear --project proj_123`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		name := args[1]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		clear, _ := cmd.Flags().GetBool("clear")
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		sourceField, _ := cmd.Flags().GetString("source-field")

		if clear && (provider != "" || model != "" || sourceField != "") {
			return fmt.Errorf("--clear is mutually exclusive with --provider, --model, --source-field")
		}

		var req api.UpdateEmbeddingConfigRequest
		if !clear {
			if provider == "" || model == "" || sourceField == "" {
				return fmt.Errorf("--provider, --model and --source-field are all required (or use --clear)")
			}
			req.EmbeddingProvider = &provider
			req.EmbeddingModel = &model
			req.SourceField = &sourceField
		}
		// If clear: all three pointers stay nil — server interprets as a clear.

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		f, err := client.UpdateVectorFieldEmbedding(projectID, collection, name, req)
		if err != nil {
			return err
		}

		fmt.Printf("\nVector field updated\n")
		fmt.Printf("   Name:       %s\n", f.FieldName)
		fmt.Printf("   Collection: %s\n", collection)
		if f.EmbeddingProvider != nil && f.SourceField != nil {
			mn := "—"
			if f.EmbeddingModel != nil {
				mn = *f.EmbeddingModel
			}
			fmt.Printf("   Auto-embed: %s → %s (model: %s)\n", *f.EmbeddingProvider, *f.SourceField, mn)
		} else {
			fmt.Printf("   Auto-embed: cleared\n")
		}
		return nil
	},
}

var vfDeleteCmd = &cobra.Command{
	Use:     "delete <collection> <name>",
	Short:   "Delete a vector field (drops the index and stored vectors)",
	Example: `  koolbase vector-fields delete articles content_embedding --project proj_123`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		name := args[1]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.DeleteVectorField(projectID, collection, name); err != nil {
			return err
		}
		fmt.Printf("Vector field %q deleted from %q\n", name, collection)
		return nil
	},
}

var vfEmbedAllCmd = &cobra.Command{
	Use:   "embed-all <collection> <name>",
	Short: "Backfill embeddings for every record on this vector field",
	Long: `Walks every record in the collection and enqueues an embedding job
for any whose source field is populated and whose existing vector
is either missing or stale (source text hash changed).

Each enqueued record is embedded by the configured provider and will
incur charges. Records with an empty source field are skipped. Records
whose stored vector already matches the current source text are skipped.

Safe to re-run — idempotent. Use this after enabling auto-embed on a
collection that already has records.`,
	Example: `  koolbase vector-fields embed-all articles content_embedding --project proj_123`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		name := args[1]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		var (
			totals api.EmbedAllResult
			cursor *string
			pages  int
		)
		fmt.Println("Backfilling embeddings…")
		for {
			pages++
			res, err := client.EmbedAllVectorField(projectID, collection, name, cursor)
			if err != nil {
				fmt.Println()
				return err
			}
			totals.Scanned += res.Scanned
			totals.Enqueued += res.Enqueued
			totals.SkippedNoSource += res.SkippedNoSource
			totals.AlreadyCurrent += res.AlreadyCurrent

			fmt.Printf("\rpage %d  scanned=%d  enqueued=%d  skipped_no_source=%d  already_current=%d",
				pages, totals.Scanned, totals.Enqueued, totals.SkippedNoSource, totals.AlreadyCurrent)

			if !res.HasMore {
				break
			}
			cursor = res.NextCursor
		}
		fmt.Printf("\n\nBackfill complete\n")
		fmt.Printf("   Records scanned:     %d\n", totals.Scanned)
		fmt.Printf("   Enqueued:            %d\n", totals.Enqueued)
		fmt.Printf("   Skipped (no source): %d\n", totals.SkippedNoSource)
		fmt.Printf("   Already current:     %d\n", totals.AlreadyCurrent)
		if totals.Enqueued > 0 {
			fmt.Printf("\nJobs will process in the background. Monitor progress at:\n")
			fmt.Printf("   https://app.koolbase.com/project/%s/embeddings\n", projectID)
		}
		return nil
	},
}

var vfLexicalBackfillCmd = &cobra.Command{
	Use:   "lexical-backfill <collection> <name>",
	Short: "Backfill the BM25 lexical index for every record on this vector field",
	Long: `Walks every record in the collection and populates the lexical
(BM25) row for any whose source field is populated. Lexical rows are
written automatically on every insert/update; this command is only
needed when enabling hybrid or lexical search on a collection that
already has pre-existing records.

Unlike embed-all, lexical-backfill is synchronous — Postgres handles
the indexing, no provider calls, no charges. Safe to re-run; records
whose source text already matches the stored lexical row are skipped.`,
	Example: `  koolbase vector-fields lexical-backfill articles content_embedding --project proj_123`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		name := args[1]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		var (
			totals api.LexicalBackfillResult
			cursor *string
			jobID  *string
			pages  int
		)
		fmt.Println("Backfilling lexical index…")
		for {
			pages++
			res, err := client.LexicalBackfillVectorField(projectID, collection, name, cursor, jobID)
			if err != nil {
				fmt.Println()
				return err
			}
			totals.Scanned += res.Scanned
			totals.Updated += res.Updated
			totals.SkippedNoSource += res.SkippedNoSource
			totals.AlreadyCurrent += res.AlreadyCurrent
			if jobID == nil && res.JobID != nil {
				jobID = res.JobID
			}

			fmt.Printf("\rpage %d  scanned=%d  updated=%d  skipped_no_source=%d  already_current=%d",
				pages, totals.Scanned, totals.Updated, totals.SkippedNoSource, totals.AlreadyCurrent)

			if !res.HasMore {
				break
			}
			cursor = res.NextCursor
		}
		fmt.Printf("\n\nLexical backfill complete\n")
		fmt.Printf("   Records scanned:     %d\n", totals.Scanned)
		fmt.Printf("   Updated:             %d\n", totals.Updated)
		fmt.Printf("   Skipped (no source): %d\n", totals.SkippedNoSource)
		fmt.Printf("   Already current:     %d\n", totals.AlreadyCurrent)
		return nil
	},
}

var vfSearchCmd = &cobra.Command{
	Use:   "search <collection> <field>",
	Short: "Search a collection by vector similarity, lexical match, or hybrid",
	Long: `Run a search against a vector field. Three modes:

  semantic (default) — pure cosine over HNSW. Fuzzy / conceptual queries.
  lexical            — pure BM25 over the field's source text.
  hybrid             — vector + lexical fused via RRF (k=60). Strong default.

--min-similarity (0..100) drops weak matches server-side. Only valid for
semantic and hybrid; the server rejects it on lexical with a 400.`,
	Example: `  # Hybrid search (default for production use):
  koolbase vector-fields search articles content_embedding \
    --query "how do I configure CI/CD?" --mode hybrid --project proj_123

  # Lexical search for an exact term:
  koolbase vector-fields search articles content_embedding \
    --query "CVE-2024-1234" --mode lexical --project proj_123

  # Semantic with a similarity floor:
  koolbase vector-fields search articles content_embedding \
    --query "shipping faster" --min-similarity 70 --project proj_123`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		field := args[1]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}

		query, _ := cmd.Flags().GetString("query")
		if query == "" {
			return fmt.Errorf("--query is required")
		}
		mode, _ := cmd.Flags().GetString("mode")
		limit, _ := cmd.Flags().GetInt("limit")
		minSim, _ := cmd.Flags().GetFloat64("min-similarity")
		minSimSet := cmd.Flags().Changed("min-similarity")

		req := api.SearchSemanticRequest{
			Collection: collection,
			Field:      field,
			QueryText:  query,
			Mode:       mode,
			Limit:      limit,
		}
		if minSimSet {
			req.MinSimilarity = &minSim
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		resp, err := client.SearchSemantic(projectID, req)
		if err != nil {
			return err
		}

		fmt.Printf("\n%d result(s) for mode=%s\n\n", resp.Total, mode)
		if resp.Total == 0 {
			fmt.Println("(no matches)")
			return nil
		}
		for i, hit := range resp.Results {
			id := "(no $id)"
			if v, ok := hit.Record["$id"].(string); ok {
				id = v
			}
			// Distance can be cosine (0..2) for semantic, BM25-like for
			// lexical, RRF score for hybrid. We label the column generically.
			fmt.Printf("%2d. %s\n", i+1, id)
			fmt.Printf("    distance: %.6f\n", hit.Distance)
			// Print up to two non-$ data fields so customers can eyeball
			// hits without piping into jq. Full payload is one query
			// away via the dashboard or db get.
			shown := 0
			for k, v := range hit.Record {
				if shown >= 2 {
					break
				}
				if len(k) > 0 && k[0] == '$' {
					continue
				}
				preview := fmt.Sprintf("%v", v)
				if len(preview) > 80 {
					preview = preview[:77] + "…"
				}
				fmt.Printf("    %s: %s\n", k, preview)
				shown++
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	vfListCmd.Flags().StringP("project", "p", "", "Project ID")

	vfCreateCmd.Flags().StringP("project", "p", "", "Project ID")
	vfCreateCmd.Flags().Int("dimensions", 0, "Vector dimensions (one of 384, 768, 1024, 1536)")
	vfCreateCmd.Flags().String("distance", "cosine", "Distance metric (cosine, l2, ip)")
	vfCreateCmd.Flags().String("provider", "", "Embedding provider (gemini, openai) — requires --model and --source-field")
	vfCreateCmd.Flags().String("model", "", "Embedding model name")
	vfCreateCmd.Flags().String("source-field", "", "Record field to embed automatically")

	vfUpdateCmd.Flags().StringP("project", "p", "", "Project ID")
	vfUpdateCmd.Flags().String("provider", "", "Embedding provider (gemini, openai)")
	vfUpdateCmd.Flags().String("model", "", "Embedding model name")
	vfUpdateCmd.Flags().String("source-field", "", "Record field to embed automatically")
	vfUpdateCmd.Flags().Bool("clear", false, "Remove auto-embed config (mutually exclusive with --provider/--model/--source-field)")

	vfDeleteCmd.Flags().StringP("project", "p", "", "Project ID")
	vfEmbedAllCmd.Flags().StringP("project", "p", "", "Project ID")

	vfLexicalBackfillCmd.Flags().StringP("project", "p", "", "Project ID")

	vfSearchCmd.Flags().StringP("project", "p", "", "Project ID")
	vfSearchCmd.Flags().String("query", "", "Query text (required)")
	vfSearchCmd.Flags().String("mode", "semantic", "Search mode: semantic | lexical | hybrid")
	vfSearchCmd.Flags().Int("limit", 10, "Max number of results")
	vfSearchCmd.Flags().Float64("min-similarity", 0, "Min similarity (0..100); only valid for semantic and hybrid")

	vectorFieldsCmd.AddCommand(vfLexicalBackfillCmd)
	vectorFieldsCmd.AddCommand(vfSearchCmd)

	vectorFieldsCmd.AddCommand(vfListCmd)
	vectorFieldsCmd.AddCommand(vfCreateCmd)
	vectorFieldsCmd.AddCommand(vfUpdateCmd)
	vectorFieldsCmd.AddCommand(vfDeleteCmd)
	vectorFieldsCmd.AddCommand(vfEmbedAllCmd)
}
