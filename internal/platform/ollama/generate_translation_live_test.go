package ollama

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// TestLiveTranslation1000WordsFiveLanguages is an opt-in throughput benchmark
// against the local Ollama endpoint. The input combines two distinct halves
// of a Mike Tyson documentary script (about 1,000 words total). There is no
// cache, database, NLP, rendering, or other pipeline work in the measured
// section.
//
// Run from refactored/:
//
//	VELOX_E2E_LIVE=1 VELOX_E2E_LANGUAGES=it,pl,ru,de,es go test ./internal/platform/ollama/ -run '^TestLiveTranslation1000WordsFiveLanguages$' -count=1 -v
//
// VELOX_E2E_OLLAMA_URL and VELOX_E2E_OLLAMA_MODEL may override the local
// endpoint/model. The model warm-up is timed and reported separately; the
// translation wall measures all five targets with a bounded fan-out of 3.
func TestLiveTranslation1000WordsFiveLanguages(t *testing.T) {
	if strings.TrimSpace(os.Getenv("VELOX_E2E_LIVE")) == "" {
		t.Skip("set VELOX_E2E_LIVE=1 to benchmark a real local Ollama server")
	}

	const corpusFirstHalf = `In Brooklyn, Mike Tyson’s story began far from championship lights. As a teenager, he found structure in boxing, where practice gave his energy direction. Trainer Cus D’Amato taught him to study distance, defend carefully, and attack with purpose. Tyson’s crouched stance made it easier to slip punches and explode forward with short hooks. The style looked simple from the seats, but it depended on balance, timing, and repetition. His amateur bouts brought attention, yet the lesson was discipline: confidence had to be earned. He learned how pressure could become a tool.
Tyson turned professional in 1985 and built a reputation for speed and power. Many opponents were stopped early, so each victory added to the sense that an unstoppable force was approaching. On November 22, 1986, in Las Vegas, he faced Trevor Berbick for the WBC heavyweight title. Tyson won by second-round technical knockout and, at twenty years old, became the youngest heavyweight world champion. The record drew headlines, but the ring offered no guarantee that success would feel easy. A belt could prove achievement; it could not decide how a person handled fame, money, expectation, or the next difficult round.
During the next years, Tyson united heavyweight titles and became one of boxing’s most recognizable figures. His fights combined forward movement, compact punches, and an ability to make opponents react before they could settle into a plan. In June 1988, he met Michael Spinks in Atlantic City. The bout ended in ninety-one seconds, a striking image of Tyson at his peak. Yet a highlight is only one frame of a career. Behind the quick finish were roadwork, sparring, preparation, and a demanding approach to competition. Inside the ropes, success still depended on choices made one exchange at a time.
The defining reversal arrived in Tokyo on February 11, 1990. Tyson entered the contest against James “Buster” Douglas as the reigning, unbeaten champion, while Douglas was underestimated. The fight lasted longer than expected. Douglas used his reach, jab, and combinations to keep Tyson from controlling the distance. In the tenth round, he knocked Tyson down and won by knockout. The result became one of boxing’s best-known upsets, and it changed the story people told about invincibility. Tyson’s defeat showed that reputation could not block a punch. It also gave Douglas a place in the sport’s history, earned through resolve.
Tyson’s later career included returns to the ring, championships, setbacks, and years spent rebuilding his life. Like Muhammad Ali, he became a figure whose fame reached beyond boxing, though their careers were distinct. His record cannot be reduced to one knockout or one defeat. It holds physical talent alongside public controversy and personal struggle, and those parts should be described without turning hardship into spectacle. The lesson is not that discipline makes anyone invulnerable. Skill grows through repeated work, while fame can magnify good decisions and mistakes. Tyson remains a heavyweight figure because his rise, fall, and return invite conversation about ambition, pressure, accountability, and what a second chapter can mean.`

	const corpusSecondHalf = `Long before a referee calls the fighters to the center of the ring, a contest has already begun in preparation. Athletes build routines around conditioning, recovery, technical drills, and the careful study of an opponent. A trainer watches how a boxer moves under fatigue, when the guard opens, and whether a familiar combination leaves room for a counter. The purpose is not to predict every second. It is to give the fighter useful choices when a plan meets resistance. That work is rarely visible to an audience, yet it can shape the rhythm of an entire evening.
The rules of professional boxing create a framework for that test. Rounds have fixed limits, officials monitor safety, and judges assess effective work rather than reputation alone. A champion may enter with years of experience, but the opening bell does not award points for yesterday’s victories. Each exchange asks for attention: control the distance, protect vulnerable openings, and recognize when an opponent is changing tactics. A boxer who rushes without balance can spend strength without gaining an advantage. Patience can be active, not passive, when it keeps a competitor ready to answer.
Public attention changes the environment around a successful athlete. Interviews, business obligations, travel, and expectations compete with the quieter habits that support training. A famous name can create opportunities, but it can also make every setback feel like a public verdict. The stories told about fighters often favor simple labels—fearless, finished, unbeatable—even though real careers contain decisions that are harder to summarize. Responsible coverage separates confirmed events from interpretation. It can describe a result clearly without claiming to know what a person privately felt or what a single night meant for the rest of a life.
For a boxer, returning after a loss requires more than announcing another date. The athlete must rebuild timing, regain confidence through practice, and decide what kind of challenge is sensible. Coaches may adjust a strategy, strengthen defensive habits, or prepare for an opponent with a different reach and pace. Fans naturally remember dramatic endings, but the craft also lives in small corrections: a step away from a hook, a cleaner exit after a combination, a calmer response when the crowd grows loud. These details connect athletic ability to learning. Improvement is not guaranteed; it depends on honest review and repeated effort.
Tyson’s public story continues to attract discussion because it joins extraordinary achievement with visible difficulty and later change. Looking at that career carefully means resisting both nostalgia and easy condemnation. Boxing can reward focus and courage while exposing people to intense physical, financial, and social pressures. A useful account gives room to the sport itself, to the opponents who shaped its history, and to the consequences beyond the ropes. It does not turn a person into a myth or reduce complex events to a slogan. The record is strongest when facts remain clear and lessons are offered with humility. The bell rings, the crowd rises, and fighters return to their corners before next round.`

	corpus := corpusFirstHalf + "\n\n" + corpusSecondHalf
	wordCount := len(strings.Fields(corpus))
	if wordCount != 1000 {
		t.Fatalf("benchmark source has %d words, want exactly 1,000", wordCount)
	}
	if strings.Contains(corpusSecondHalf, "In Brooklyn, Mike Tyson’s story began") {
		t.Fatal("benchmark halves must contain distinct text, not duplicate the same corpus")
	}

	languages := []string{"it", "pl", "ru", "de", "es"}
	if raw := strings.TrimSpace(os.Getenv("VELOX_E2E_LANGUAGES")); raw != "" {
		languages = languages[:0]
		for _, code := range strings.Split(raw, ",") {
			code = strings.TrimSpace(code)
			if code != "" && code != "en" {
				languages = append(languages, code)
			}
		}
	}
	if len(languages) != 5 {
		t.Fatalf("benchmark requires exactly five target languages, got %v", languages)
	}

	endpoint := strings.TrimSpace(os.Getenv("VELOX_E2E_OLLAMA_URL"))
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434"
	}
	model := strings.TrimSpace(os.Getenv("VELOX_E2E_OLLAMA_MODEL"))
	if model == "" {
		model = "gemma4:e4b"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	ollamaClient := client.NewClient(endpoint, model, 600)
	if !ollamaClient.CheckHealth(ctx) {
		t.Fatalf("Ollama endpoint %s is not healthy", endpoint)
	}
	availableModels, err := ollamaClient.ListModels(ctx)
	if err != nil {
		t.Fatalf("list Ollama models: %v", err)
	}
	modelAvailable := false
	for _, available := range availableModels {
		if available.Name == model || strings.HasPrefix(available.Name, model+":") {
			modelAvailable = true
			break
		}
	}
	if !modelAvailable {
		t.Fatalf("model %q is not installed at %s", model, endpoint)
	}

	generator := NewGenerator(ollamaClient)
	warmStart := time.Now()
	if err := ollamaClient.WarmModel(ctx, model); err != nil {
		t.Fatalf("warm Ollama model %q: %v", model, err)
	}
	warmWall := time.Since(warmStart)
	admissionBefore := ollamaClient.AdmissionStats()

	type result struct {
		language string
		words    int
		wall     time.Duration
	}
	totalStart := time.Now()
	results, err := concurrent.Map(ctx, languages, 3, func(callCtx context.Context, _ int, language string) (result, error) {
		start := time.Now()
		text, translateErr := generator.TranslateTextWithModel(callCtx, corpus, language, model)
		if translateErr != nil {
			return result{}, fmt.Errorf("translate en->%s: %w", language, translateErr)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return result{}, fmt.Errorf("translate en->%s returned empty output", language)
		}
		return result{language: language, words: len(strings.Fields(text)), wall: time.Since(start)}, nil
	})
	translationWall := time.Since(totalStart)
	if err != nil {
		t.Fatalf("live translation benchmark failed: %v", err)
	}

	t.Logf("benchmark source_words=%d targets=%v model=%s endpoint=%s max_concurrency=3 cache=disabled", wordCount, languages, model, endpoint)
	t.Logf("model_warmup_wall=%s (reported separately; not included in translation wall)", warmWall.Round(time.Millisecond))
	for _, translated := range results {
		t.Logf("en->%s translated_words=%d wall=%s", translated.language, translated.words, translated.wall.Round(time.Millisecond))
	}
	admissionAfter := ollamaClient.AdmissionStats()
	t.Logf("translation_wall=%s; admission_limit=%d peak=%d deferred_delta=%d", translationWall.Round(time.Millisecond), admissionAfter.Limit, admissionAfter.Peak, admissionAfter.Deferred-admissionBefore.Deferred)
}
