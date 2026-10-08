package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSARIFValidatesAndMapsToRepositoryFiles(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	r.Write("util/my file.go", "package util\n\nimport \"fmt\"\n\nfunc x(n string) string { return fmt.Sprintf(\"%d\", n) }\n")
	runReview(t, r.Dir)
	run(t, r.Dir, "tag", "1", "irrelevant")
	code, out, errs := run(t, r.Dir, "review", "--format", "sarif")
	if code != ExitBlocking {
		t.Fatalf("SARIF keeps the exit code: %d %s", code, errs)
	}
	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI       string `json:"uri"`
							URIBaseID string `json:"uriBaseId"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				Suppressions []any             `json:"suppressions"`
				Fingerprints map[string]string `json:"partialFingerprints"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatal(err)
	}
	results := log.Runs[0].Results
	if log.Version != "2.1.0" || len(results) != 2 {
		t.Fatalf("%s", out)
	}
	suppressed := 0
	for _, res := range results {
		loc := res.Locations[0].PhysicalLocation
		path := strings.ReplaceAll(loc.ArtifactLocation.URI, "%20", " ")
		if loc.ArtifactLocation.URIBaseID != "SRCROOT" || res.Fingerprints["pistos/v1"] == "" {
			t.Fatalf("%+v", res)
		}
		if _, err := os.Stat(filepath.Join(r.Dir, path)); err != nil || loc.Region.StartLine < 1 {
			t.Fatalf("SARIF location must map to a repository file: %s", loc.ArtifactLocation.URI)
		}
		suppressed += len(res.Suppressions)
	}
	if suppressed != 1 {
		t.Fatal("the tagged issue must stay in SARIF as suppressed")
	}

	if _, err := exec.LookPath("python3"); err != nil {
		skipLive(t, "python3 unavailable; SARIF schema validation skipped")
	}
	file := filepath.Join(t.TempDir(), "out.sarif")
	os.WriteFile(file, []byte(out), 0o644)
	schema, _ := filepath.Abs("../report/testdata/sarif-schema-2.1.0.json")
	cmd := exec.Command("python3", "-c", "import json,sys,jsonschema; jsonschema.validate(json.load(open(sys.argv[1])), json.load(open(sys.argv[2])))", file, schema)
	if b, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(b), "No module named") {
			skipLive(t, "python jsonschema unavailable; SARIF schema validation skipped")
		}
		t.Fatalf("SARIF does not validate against the 2.1.0 schema: %s", b)
	}
}
