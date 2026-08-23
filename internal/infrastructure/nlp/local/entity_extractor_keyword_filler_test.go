package local

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fillerScene is one real-world news/celebrity sentence. Every scene carries a
// multi-word proper name (so entity recall is meaningful) plus natural filler
// vocabulary ("after", "months", "finally", "would", "during", "year", ...).
// The keyword selector must keep the concrete nouns/names and never promote a
// stopword or function word into ImportantWords.
type fillerScene struct {
	name       string
	source     string
	wantPerson string
}

// The 50 real scenes for Test 17. This is deliberately a broad, varied corpus:
// wrestling, sports, music, film, tech, politics, science and climate. The
// semantic-quality requirement ("quality matters more than a unit test") is met
// by (a) spanning many domains and (b) asserting the hard negative property —
// no stopword/function word leak — on every one of them, not by pinning exact
// keyword values (which the 10-scene cert test already does separately).
var keywordFillerScenes = []fillerScene{
	{name: "cody-rhodes", source: "After months of speculation, Cody Rhodes finally confirmed that he would return at WrestleMania.", wantPerson: "Cody Rhodes"},
	{name: "elon-musk", source: "Elon Musk spent months testing the new Tesla prototype before he finally unveiled it to the public.", wantPerson: "Elon Musk"},
	{name: "taylor-swift", source: "Taylor Swift would eventually announce her world tour after months of quiet preparation.", wantPerson: "Taylor Swift"},
	{name: "serena-williams", source: "Serena Williams finally returned to the court after a year of training in New York.", wantPerson: "Serena Williams"},
	{name: "dwayne-johnson", source: "Dwayne Johnson confirmed that he would star in the sequel after months of negotiations.", wantPerson: "Dwayne Johnson"},
	{name: "lionel-messi", source: "Lionel Messi finally signed with the club after months of transfer rumors.", wantPerson: "Lionel Messi"},
	{name: "cristiano-ronaldo", source: "Cristiano Ronaldo would score again in the final minutes of the match in Madrid.", wantPerson: "Cristiano Ronaldo"},
	{name: "lebron-james", source: "LeBron James finally broke the record during a game in Los Angeles.", wantPerson: "LeBron James"},
	{name: "beyonce-knowles", source: "Beyoncé Knowles announced her album after months of silence from the studio.", wantPerson: "Beyoncé Knowles"},
	{name: "oprah-winfrey", source: "Oprah Winfrey would host the interview after months of planning with her team.", wantPerson: "Oprah Winfrey"},
	{name: "jeff-bezos", source: "Jeff Bezos finally launched the rocket after months of delays at the spaceport.", wantPerson: "Jeff Bezos"},
	{name: "mark-zuckerberg", source: "Mark Zuckerberg confirmed the new headset would ship within months.", wantPerson: "Mark Zuckerberg"},
	{name: "bill-gates", source: "Bill Gates warned that the climate fund would need more money this year.", wantPerson: "Bill Gates"},
	{name: "angela-merkel", source: "Angela Merkel finally published her memoir after months of writing in Berlin.", wantPerson: "Angela Merkel"},
	{name: "emmanuel-macron", source: "Emmanuel Macron would address the nation after days of protest in Paris.", wantPerson: "Emmanuel Macron"},
	{name: "novak-djokovic", source: "Novak Djokovic finally won the title after months of rehabilitation.", wantPerson: "Novak Djokovic"},
	{name: "rafael-nadal", source: "Rafael Nadal announced he would retire after a final season of competition.", wantPerson: "Rafael Nadal"},
	{name: "tom-cruise", source: "Tom Cruise finally performed the stunt after months of training for the film.", wantPerson: "Tom Cruise"},
	{name: "leonardo-dicaprio", source: "Leonardo DiCaprio urged leaders to act before the climate crisis worsened.", wantPerson: "Leonardo DiCaprio"},
	{name: "greta-thunberg", source: "Greta Thunberg would speak at the summit after weeks of preparation.", wantPerson: "Greta Thunberg"},
	{name: "marie-curie", source: "Marie Curie finally received recognition after years of research in Paris.", wantPerson: "Marie Curie"},
	{name: "albert-einstein", source: "Albert Einstein would publish the theory after months of careful thought.", wantPerson: "Albert Einstein"},
	{name: "steve-jobs", source: "Steve Jobs finally revealed the device after months of secrecy at Apple.", wantPerson: "Steve Jobs"},
	{name: "michael-jordan", source: "Michael Jordan would return to the court after a short retirement in Chicago.", wantPerson: "Michael Jordan"},
	{name: "kobe-bryant", source: "Kobe Bryant finally retired after two decades with the team in Los Angeles.", wantPerson: "Kobe Bryant"},
	{name: "usain-bolt", source: "Usain Bolt would break the record again during the championship in London.", wantPerson: "Usain Bolt"},
	{name: "simone-biles", source: "Simone Biles finally returned to competition after a year away from the sport.", wantPerson: "Simone Biles"},
	{name: "naomi-osaka", source: "Naomi Osaka announced she would compete again after months of recovery.", wantPerson: "Naomi Osaka"},
	{name: "emma-raducanu", source: "Emma Raducanu finally won her match after months of coaching changes.", wantPerson: "Emma Raducanu"},
	{name: "lewis-hamilton", source: "Lewis Hamilton would chase another title after a difficult year for the team.", wantPerson: "Lewis Hamilton"},
	{name: "max-verstappen", source: "Max Verstappen finally clinched the championship during the final race.", wantPerson: "Max Verstappen"},
	{name: "fernando-alonso", source: "Fernando Alonso confirmed he would race again after months of speculation.", wantPerson: "Fernando Alonso"},
	{name: "roger-federer", source: "Roger Federer finally said goodbye after a legendary career in tennis.", wantPerson: "Roger Federer"},
	{name: "andy-murray", source: "Andy Murray would continue playing after months of hip rehabilitation.", wantPerson: "Andy Murray"},
	{name: "jon-bon-jovi", source: "Jon Bon Jovi finally released the single after months in the recording studio.", wantPerson: "Jon Bon Jovi"},
	{name: "bruce-springsteen", source: "Bruce Springsteen announced he would tour again after years away.", wantPerson: "Bruce Springsteen"},
	{name: "adele-adkins", source: "Adele Adkins finally returned with an album after months of anticipation.", wantPerson: "Adele Adkins"},
	{name: "ed-sheeran", source: "Ed Sheeran would headline the festival after months of schedule changes.", wantPerson: "Ed Sheeran"},
	{name: "quentin-tarantino", source: "Quentin Tarantino finally finished the script after months of rewrites.", wantPerson: "Quentin Tarantino"},
	{name: "christopher-nolan", source: "Christopher Nolan confirmed the film would use practical effects during production.", wantPerson: "Christopher Nolan"},
	{name: "martin-scorsese", source: "Martin Scorsese would direct the documentary after months of research.", wantPerson: "Martin Scorsese"},
	{name: "steven-spielberg", source: "Steven Spielberg finally announced his next project after a year of silence.", wantPerson: "Steven Spielberg"},
	{name: "george-martin", source: "George Martin confirmed the book would arrive after years of delays.", wantPerson: "George Martin"},
	{name: "keanu-reeves", source: "Keanu Reeves finally returned to the franchise after months of rumors.", wantPerson: "Keanu Reeves"},
	{name: "vin-diesel", source: "Vin Diesel announced the sequel would premiere within months.", wantPerson: "Vin Diesel"},
	{name: "harrison-ford", source: "Harrison Ford finally wrapped the film after months of shooting in London.", wantPerson: "Harrison Ford"},
	{name: "morgan-freeman", source: "Morgan Freeman would narrate the series after months of negotiations.", wantPerson: "Morgan Freeman"},
	{name: "denzel-washington", source: "Denzel Washington finally directed the play after years of preparation.", wantPerson: "Denzel Washington"},
	{name: "will-smith", source: "Will Smith confirmed he would produce the documentary after months of planning.", wantPerson: "Will Smith"},
	{name: "robert-downey", source: "Robert Downey finally returned to the role after months of fan speculation.", wantPerson: "Robert Downey"},
}

// requireContainsPerson asserts the expected multi-word person was captured
// among the scene's extracted persons. It does not require it to be the only
// person (a scene may legitimately carry other proper names), only that the
// primary subject was not dropped.
func requireContainsPerson(t *testing.T, result *scriptpkg.EntityResult, want string) {
	t.Helper()
	for _, p := range result.Persons {
		if p.Value == want {
			return
		}
	}
	t.Fatalf("persons = %+v, want %q among them", result.Persons, want)
}

// TestKeywordSelection50RealScenesNoFiller is the certification for Test 17:
// keyword selection across 50 real scenes never returns a stopword or function
// word (the exact regressions are "months", "finally", "would"), every keyword
// is grounded in the source text, and the primary subject is still recognized.
func TestKeywordSelection50RealScenesNoFiller(t *testing.T) {
	profile := linguistics.DefaultLexicon().Resolve("en")
	if len(keywordFillerScenes) != 50 {
		t.Fatalf("expected exactly 50 filler scenes, got %d", len(keywordFillerScenes))
	}
	for _, sc := range keywordFillerScenes {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			result := extractEN(t, sc.source)
			lowerSource := strings.ToLower(sc.source)
			for _, word := range result.ImportantWords {
				lower := strings.ToLower(word)
				switch lower {
				case "months", "finally", "would":
					t.Fatalf("forbidden filler %q leaked into keywords: %v", lower, result.ImportantWords)
				}
				if _, stop := profile.StopWords[lower]; stop {
					t.Fatalf("stopword %q leaked into keywords: %v", lower, result.ImportantWords)
				}
				if _, function := profile.FunctionWords[lower]; function {
					t.Fatalf("function word %q leaked into keywords: %v", lower, result.ImportantWords)
				}
				if !strings.Contains(lowerSource, lower) {
					t.Fatalf("keyword %q is not grounded in the source text", lower)
				}
			}
			requireContainsPerson(t, result, sc.wantPerson)
		})
	}
}
