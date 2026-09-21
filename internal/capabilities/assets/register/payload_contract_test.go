package register

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// payloadDir is the home of the operator-authored register-batch payloads
// (added with the CLIPS timestamp flow). The test walks every
// *.register-batch.json in that directory so a payload that drifts from the
// wire contract fails the build instead of failing a live run.
const payloadDir = "../../../../tests/operational/payloads"

// TestOperationalRegisterPayloads_MatchBatchContract pins the
// operator-supplied payloads against the REAL handler contract:
//
//  1. the body decodes into BatchRegisterRequest (the exact DTO the handler
//     binds via c.ShouldBindJSON);
//  2. clips[] is non-empty and every clip carries the binding-required url
//     plus a name that yields a readable Drive filename;
//  3. effectiveFolderID(&req) is non-empty, because the composition root's
//     Drive gate only routes folder_id traffic when Drive is wired — a
//     payload without folder_id would silently land in the default root;
//  4. every window is strictly positive and every URL is a real YouTube
//     watch link (the same urlutil gate the handler applies pre-flight);
//  5. expandClipsBySegments returns the SAME number of clips — i.e. the
//     payloads ask for whole highlight windows and are NOT fanned out into
//     N-second children. This is the property that distinguishes the clips
//     path from /api/stock-pipeline/run, whose explicit planner auto-splits
//     any clip >= 60s into 5-second children. These payloads contain
//     windows well over 60s, so a regression there would mis-cut every one
//     of them.
func TestOperationalRegisterPayloads_MatchBatchContract(t *testing.T) {
	entries, err := os.ReadDir(payloadDir)
	if err != nil {
		t.Fatalf("read payload dir %s: %v", payloadDir, err)
	}

	seen := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".register-batch.json") {
			continue
		}
		seen++
		path := filepath.Join(payloadDir, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var req BatchRegisterRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatalf("payload does not decode into BatchRegisterRequest: %v", err)
			}
			if len(req.Clips) == 0 {
				t.Fatal("clips[] is empty (handler answers 400 \"clips list is empty\")")
			}
			if got := effectiveFolderID(&req); got == "" {
				t.Fatal("effectiveFolderID is empty: the payload would bypass the Drive folder gate and land in the default root")
			}

			for i, clip := range req.Clips {
				where := clip.Name
				if where == "" {
					where = fmt.Sprintf("clip[%d]", i)
				}
				if strings.TrimSpace(clip.URL) == "" {
					t.Errorf("%s: url is empty (binding:\"required\" -> 400)", where)
				} else if _, err := urlutil.ExtractVideoID(clip.URL); err != nil {
					t.Errorf("%s: url %q is not a YouTube watch link: %v", where, clip.URL, err)
				}
				if strings.TrimSpace(clip.Name) == "" {
					t.Errorf("clip[%d]: name is empty -> anonymous Drive filename", i)
				}
				if clip.End <= clip.Start {
					t.Errorf("%s: window [%v,%v] is not positive", where, clip.Start, clip.End)
				}
				if clip.Source != "youtube" {
					t.Errorf("%s: source = %q, want \"youtube\" (default otherwise becomes youtube-manual)", where, clip.Source)
				}
			}

			expanded, err := expandClipsBySegments(req.Clips)
			if err != nil {
				t.Fatalf("seconds_per_segment expansion rejected the payload: %v", err)
			}
			if len(expanded) != len(req.Clips) {
				t.Fatalf("payload asks for whole windows but expandClipsBySegments produced %d clips from %d (each clip must keep ONE window)",
					len(expanded), len(req.Clips))
			}
			for i := range expanded {
				if expanded[i].Start != req.Clips[i].Start || expanded[i].End != req.Clips[i].End {
					t.Errorf("clip %d window changed by expansion: [%v,%v] -> [%v,%v]",
						i, req.Clips[i].Start, req.Clips[i].End, expanded[i].Start, expanded[i].End)
				}
			}
		})
	}

	if seen == 0 {
		t.Fatalf("no *.register-batch.json payloads found in %s", payloadDir)
	}
}
