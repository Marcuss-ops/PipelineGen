package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/entitycatalog"
)

// liveHundredPersonNames is deliberately varied: athletes, politicians,
// actors, scientists and historical figures. The catalog is PERSON-only, so a
// place or object query would not exercise the identity surface this test is
// certifying.
var liveHundredPersonNames = []string{
	"Michael Jordan", "Taylor Swift", "Elon Musk", "Tim Cook", "Donald Trump",
	"Joe Biden", "Giorgia Meloni", "Sergio Mattarella", "Cristiano Ronaldo", "Lionel Messi",
	"Mike Tyson", "Tom Cruise", "Meryl Streep", "David Bowie", "Madonna",
	"Bruce Springsteen", "Barack Obama", "Angela Merkel", "Emmanuel Macron", "Vladimir Putin",
	"Xi Jinping", "Narendra Modi", "Pope Francis", "Queen Elizabeth II", "Prince William",
	"Kate Middleton", "Leonardo DiCaprio", "Brad Pitt", "Angelina Jolie", "Johnny Depp",
	"Scarlett Johansson", "Robert Downey Jr", "Chris Hemsworth", "Gal Gadot", "Dwayne Johnson",
	"Keanu Reeves", "Sandra Bullock", "Julia Roberts", "Nicole Kidman", "Hugh Jackman",
	"Serena Williams", "Roger Federer", "Rafael Nadal", "Novak Djokovic", "Usain Bolt",
	"Michael Phelps", "Simone Biles", "Lewis Hamilton", "Ayrton Senna", "Diego Maradona",
	"Pele", "Zinedine Zidane", "David Beckham", "Kylian Mbappe", "Erling Haaland",
	"Stephen Curry", "LeBron James", "Kobe Bryant", "Shaquille O'Neal", "Tiger Woods",
	"Bill Gates", "Steve Jobs", "Mark Zuckerberg", "Jeff Bezos", "Warren Buffett",
	"Jack Ma", "Sundar Pichai", "Satya Nadella", "Sam Altman", "Ada Lovelace",
	"Alan Turing", "Albert Einstein", "Marie Curie", "Isaac Newton", "Charles Darwin",
	"Nicola Tesla", "Thomas Edison", "Galileo Galilei", "Leonardo da Vinci", "Michelangelo",
	"Vincent van Gogh", "Pablo Picasso", "Frida Kahlo", "Salvador Dali", "Ernest Hemingway",
	"George Orwell", "Jane Austen", "William Shakespeare", "Dante Alighieri", "Alessandro Manzoni",
	"Umberto Eco", "Italo Calvino", "Federico Fellini", "Sophia Loren", "Monica Bellucci",
	"Roberto Benigni", "Ennio Morricone", "Luciano Pavarotti", "Andrea Bocelli", "Vasco Rossi",
}

// TestLiveEntityImageCatalogHundredIdentities is the acceptance gate for the
// Kaufmann class of failure: 100 different PERSON identities must produce a
// durable catalog row from a live provider search. It asserts that the
// identity filter does not throw away generic editorial filenames before the
// visual verifier can see them, and that the catalog is populated for each
// identity rather than only for the one manually tested name.
//
// Run with:
//
//	PERSON_CATALOG_LIVE=1 go test ./internal/capabilities/scripts/adapters \
//	  -run '^TestLiveEntityImageCatalogHundredIdentities$' -v -count=1 -timeout 20m
func TestLiveEntityImageCatalogHundredIdentities(t *testing.T) {
	if os.Getenv("PERSON_CATALOG_LIVE") != "1" {
		t.Skip("set PERSON_CATALOG_LIVE=1 to run the 100-identity live catalog test")
	}
	if len(liveHundredPersonNames) != 100 {
		t.Fatalf("identity fixture has %d names, want 100", len(liveHundredPersonNames))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	resetEntityImageCatalogCaches()
	db := openPersistentEntityImageCatalog(t, filepath.Join(t.TempDir(), "live-100-identities.db"))
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	searcher := newLiveJobImageSearcher(t)
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)
	plan := liveJobPlan()

	failures := make([]string, 0)
	providerSearches := 0
	for i, name := range liveHundredPersonNames {
		input := liveJobInput(fmt.Sprintf("live-100-%03d", i), name)
		result, err := processor.Process(ctx, plan, input)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: process: %v", name, err))
			continue
		}
		if len(result.VidRushSegments) != 1 {
			failures = append(failures, fmt.Sprintf("%s: segments=%d", name, len(result.VidRushSegments)))
			continue
		}
		segment := result.VidRushSegments[0]
		providerSearches += segment.Cache.InternetImagesProviderSearches
		if len(segment.Assets.SecondaryImages) == 0 {
			failures = append(failures, fmt.Sprintf("%s: search returned zero candidates", name))
			continue
		}
		identity, identityErr := entitycatalog.CanonicalizePersonName(name)
		if identityErr != nil {
			failures = append(failures, fmt.Sprintf("%s: canonical identity: %v", name, identityErr))
			continue
		}
		rows, listErr := repo.ListCandidates(ctx, identity.CanonicalEntityID, 20)
		if listErr != nil {
			failures = append(failures, fmt.Sprintf("%s: list catalog: %v", name, listErr))
			continue
		}
		if len(rows) == 0 {
			failures = append(failures, fmt.Sprintf("%s: catalog row missing", name))
		}
	}

	t.Logf("live catalog: %d/%d identities populated, provider searches=%d",
		len(liveHundredPersonNames)-len(failures), len(liveHundredPersonNames), providerSearches)
	for _, failure := range failures {
		t.Logf("  FAIL %s", failure)
	}
	// Allow a small number of genuinely unsearchable names, but refuse the
	// systemic zero-candidate failure this test exists to catch.
	if len(failures) > 10 {
		t.Fatalf("%d of %d identities failed to populate the catalog", len(failures), len(liveHundredPersonNames))
	}
}
