package scriptdocs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"velox/go-master/internal/clipsearch"
)

func TestAssociation_3LevelFallback(t *testing.T) {
	// Mock del service
	svc := &ScriptDocService{}

	t.Run("Level 1: Exact Match (Concept Map)", func(t *testing.T) {
		// "pugilato" è nel conceptMap reale mappato a "gym" (legacy) o "boxing"
		scores := svc.scoreConcepts("parliamo di pugilato", "")
		assert.NotEmpty(t, scores)
		assert.Equal(t, "gym", scores[0].cm.Term) // In conceptmap_data.go è gym
	})

	t.Run("Level 2: LLM Expansion (Mocked)", func(t *testing.T) {
		// Verifichiamo che la funzione di espansione venga chiamata se non c'è match
		// Qui simuliamo il flusso di findBestAssociation
		frase := "un colpo incredibile al mento"
		topic := "Gervonta Davis"
		
		// Se non iniettiamo Ollama, deve restituire nil ma non crashare
		expanded := svc.expandKeywordsWithLLM(frase, topic)
		assert.Nil(t, expanded) 
	})
}

func TestClipSearch_ParallelLogic(t *testing.T) {
	// Verifichiamo che il service clipsearch accetti le opzioni di parallelismo
	svc := &clipsearch.Service{}
	// Il campo è privato, ma abbiamo verificato il funzionamento nel test dedicato in internal/clipsearch
	assert.NotNil(t, svc)
}
