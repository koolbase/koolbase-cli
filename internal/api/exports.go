package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LatestExport describes what the Designer last generated for a project,
// without downloading it.
//
// Note DocumentHash: this is the latest EXPORT, not the latest document.
// Someone who edited their design and did not export again should be
// told that rather than left believing they pulled their newest work.
type LatestExport struct {
	ID              string            `json:"id"`
	DocumentID      string            `json:"document_id"`
	DocumentHash    string            `json:"document_hash"`
	ExporterVersion string            `json:"exporter_version"`
	ExportHash      string            `json:"export_hash"`
	CreatedAt       time.Time         `json:"created_at"`
	Manifest        map[string]string `json:"manifest"`
}

func (c *Client) LatestExport(projectID string) (*LatestExport, error) {
	data, status, err := c.do("GET", "/v1/projects/"+projectID+"/exports/latest", nil)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, fmt.Errorf("this project has no export yet — export once from the Designer")
	}
	if status != 200 {
		return nil, fmt.Errorf("could not read the latest export (%d): %s", status, string(data))
	}
	var out LatestExport
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadExport fetches the artifact itself.
//
// Two calls -- a presigned URL, then the object store -- rather than
// streaming through the API: a large project would otherwise hold an API
// request open for the whole transfer.
func (c *Client) DownloadExport(projectID, exportID string) ([]byte, error) {
	data, status, err := c.do(
		"GET",
		"/v1/projects/"+projectID+"/exports/"+exportID+"/download",
		nil,
	)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("could not fetch the export (%d): %s", status, string(data))
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	if body.URL == "" {
		return nil, fmt.Errorf("the API returned no download URL")
	}

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Get(body.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("downloading the export failed (%d)", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
