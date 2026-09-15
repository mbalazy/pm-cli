package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// Dismiss/restore on the queue and the solo endpoints (pm-cli-125/136).
func TestAttentionDismissAndSolo(t *testing.T) {
	store := newTestStore(t)
	dir := filepath.Join(store.ProjectDir("test"), storage.ShiftDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	closed := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, "2026-09-13-abc.md"), []byte("# solo abc\n## Status: closed "+closed+"\n## Queue\nt-1 · First · done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-13-abc-report.md"), []byte("# Raport: abc\n\n## 1. Co z taskami\n\n### t-1 First\n\n- Stan: **zrobione**. Działa.\n\n## TL;DR\n\nZrobione. Od Ciebie: wypchnij gałąź.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newServer(t, store, Options{})

	soloSection := func() map[string]any {
		t.Helper()
		for _, s := range getJSON(t, srv.URL+"/api/attention", 200)["sections"].([]any) {
			if sec := s.(map[string]any); sec["name"] == "solo_reports" {
				return sec
			}
		}
		t.Fatal("no solo_reports section")
		return nil
	}
	sec := soloSection()
	rows := sec["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("solo rows = %v", sec)
	}
	row := rows[0].(map[string]any)
	acts := row["actions"].([]any)
	if row["shift"] != "abc" || len(acts) != 2 || acts[0] != "open_report" || acts[1] != "dismiss" ||
		row["title"] != "abc" || row["reason"] != "1 done · Od Ciebie: wypchnij gałąź." {
		t.Fatalf("row = %v", row)
	}

	body := `{"rows":[{"section":"solo_reports","project":"test","shift":"abc","since":"` + row["since"].(string) + `"}]}`
	if m := postJSON(t, srv.URL+"/api/attention/dismiss", body, 200); m["dismissed"].(float64) != 1 {
		t.Fatalf("dismiss = %v", m)
	}
	sec = soloSection()
	if len(sec["rows"].([]any)) != 0 || sec["dismissed"].(float64) != 1 {
		t.Fatalf("after dismiss = %v", sec)
	}
	postJSON(t, srv.URL+"/api/attention/dismiss", `{"rows":[{"section":"waiting","project":"test","task_id":"t-1"}]}`, 400)
	postJSON(t, srv.URL+"/api/attention/dismiss", `{"rows":[]}`, 400)
	if m := postJSON(t, srv.URL+"/api/attention/restore", `{"section":"solo_reports"}`, 200); m["restored"].(float64) != 1 {
		t.Fatalf("restore = %v", m)
	}
	if len(soloSection()["rows"].([]any)) != 1 {
		t.Fatal("restore did not bring the row back")
	}

	shifts := getJSON(t, srv.URL+"/api/solo", 200)["shifts"].([]any)
	if len(shifts) != 1 || shifts[0].(map[string]any)["id"] != "abc" {
		t.Fatalf("shifts = %v", shifts)
	}
	rep := getJSON(t, srv.URL+"/api/solo/test/abc/report", 200)
	if rep["kind"] != "report" || !strings.Contains(rep["markdown"].(string), "# Raport: abc") {
		t.Fatalf("report = %v", rep)
	}
	dg := rep["digest"].(map[string]any)
	if tasks := dg["tasks"].([]any); len(tasks) != 1 || tasks[0].(map[string]any)["outcome"] != "done" || dg["next"] != "Od Ciebie: wypchnij gałąź." {
		t.Fatalf("digest = %v", dg)
	}
	if sum := rep["shift"].(map[string]any)["summary"].(map[string]any); sum["done"].(float64) != 1 || sum["title"] != "abc" {
		t.Fatalf("summary = %v", sum)
	}
	getJSON(t, srv.URL+"/api/solo/test/nosuch/report", 404)
	getJSON(t, srv.URL+"/api/solo/test/..%2F..%2Fconfig/report", 404)
}
