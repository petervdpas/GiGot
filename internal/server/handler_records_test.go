package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// seedMultipleRecords seeds three records under storage/addresses/ so
// record-query tests can exercise filter/sort/limit in one place.
func seedMultipleRecords(t *testing.T, srv *Server, repo string) {
	t.Helper()
	if err := srv.git.InitBare(repo); err != nil {
		t.Fatalf("init %s: %v", repo, err)
	}
	seedFile(t, srv, repo, formidableMarkerPath, formidableMarkerBody, "stamp marker")
	seedFile(t, srv, repo, "storage/addresses/oak.meta.json",
		recordJSON(t, "2025-01-01T00:00:00Z", map[string]any{"city": "London", "count": 7}),
		"seed oak")
	seedFile(t, srv, repo, "storage/addresses/elm.meta.json",
		recordJSON(t, "2025-01-02T00:00:00Z", map[string]any{"city": "Paris", "count": 3}),
		"seed elm")
	seedFile(t, srv, repo, "storage/addresses/ash.meta.json",
		recordJSON(t, "2025-01-03T00:00:00Z", map[string]any{"city": "London", "count": 12}),
		"seed ash")
	// Also drop a file under images/ so we can confirm it's not
	// returned as a record.
	seedFile(t, srv, repo, "storage/addresses/images/photo.jpg",
		"\x89PNG\r\n", "seed binary")
}

func decodeRecordQuery(t *testing.T, rr *httptest.ResponseRecorder) RecordQueryResponse {
	t.Helper()
	var resp struct {
		Version string           `json:"version"`
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rr.Body.String())
	}
	return RecordQueryResponse{Version: resp.Version, Records: resp.Records}
}

func TestRecordsQuery_ListsAllAtHead(t *testing.T) {
	srv := testServer(t)
	repo := "rq-list"
	seedMultipleRecords(t, srv, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/repos/"+repo+"/records/addresses", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeRecordQuery(t, rr)
	if resp.Version == "" {
		t.Error("version is empty")
	}
	if len(resp.Records) != 3 {
		t.Errorf("got %d records, want 3", len(resp.Records))
	}
}

func TestRecordsQuery_FilterByEquality(t *testing.T) {
	srv := testServer(t)
	repo := "rq-eq"
	seedMultipleRecords(t, srv, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/repos/"+repo+"/records/addresses?where=city=London", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeRecordQuery(t, rr)
	if len(resp.Records) != 2 {
		t.Errorf("got %d records, want 2", len(resp.Records))
	}
	for _, rec := range resp.Records {
		data := rec["data"].(map[string]any)
		if data["city"] != "London" {
			t.Errorf("unexpected city %v", data["city"])
		}
	}
}

func TestRecordsQuery_NumericRangeSortLimit(t *testing.T) {
	srv := testServer(t)
	repo := "rq-range"
	seedMultipleRecords(t, srv, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/repos/"+repo+"/records/addresses?where=count>5&sort=-count&limit=1", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeRecordQuery(t, rr)
	if len(resp.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(resp.Records))
	}
	data := resp.Records[0]["data"].(map[string]any)
	if got := data["count"]; got.(float64) != 12 {
		t.Errorf("top record count = %v, want 12", got)
	}
}

func TestRecordsQuery_IgnoresImagesSubdir(t *testing.T) {
	srv := testServer(t)
	repo := "rq-img"
	seedMultipleRecords(t, srv, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/repos/"+repo+"/records/addresses", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	resp := decodeRecordQuery(t, rr)
	// Three records seeded — binary under images/ must not leak in.
	if len(resp.Records) != 3 {
		t.Errorf("got %d records, want 3 (images dir leaked?)", len(resp.Records))
	}
}

func TestRecordsQuery_InvalidFilter(t *testing.T) {
	srv := testServer(t)
	repo := "rq-bad"
	seedMultipleRecords(t, srv, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/repos/"+repo+"/records/addresses?where=bogus", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestRecordsQuery_RejectsSlashInTemplate(t *testing.T) {
	srv := testServer(t)
	repo := "rq-slash"
	seedMultipleRecords(t, srv, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/repos/"+repo+"/records/addresses/extra", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}
