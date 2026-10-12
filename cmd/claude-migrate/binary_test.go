package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onlyilkr/claude-migrate/internal/report"
)

func TestBinaryPlanJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "claude-migrate")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	h := seed(t)
	out, err := exec.Command(bin, "plan", "--json", "--home", h.Dir).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var pl report.PlanJSON
	if err := json.Unmarshal(out, &pl); err != nil || pl.Counts.Copy != 3 {
		t.Fatalf("plan = %+v, err = %v\n%s", pl.Counts, err, out)
	}
}
