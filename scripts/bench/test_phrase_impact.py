import json
import unittest

from scripts.bench.phrase_impact import align_sentence_times, read_markdown, split_sentences
from pathlib import Path


class PhraseImpactParsingTests(unittest.TestCase):
    def test_embedding_batches_preserve_order_and_validate_metadata(self):
        from unittest import mock

        import scripts.bench.elon_phrase_impact as benchmark

        seen = []

        def response_for(request, timeout):
            payload = json.loads(request.data.decode("utf-8"))
            seen.append(payload)
            count = len(payload["texts"])
            body = {
                "embeddings": [[float(index)] * 4 for index in range(count)],
                "dimensions": 4,
                "count": count,
                "model": "test-model",
                "model_version": "test-revision",
                "contract_hash": "test-contract",
            }
            response = mock.MagicMock()
            response.read.return_value = json.dumps(body).encode("utf-8")
            response.__enter__.return_value = response
            response.__exit__.return_value = None
            return response

        with mock.patch.object(benchmark.urllib.request, "urlopen", side_effect=response_for):
            vectors, metadata = benchmark.embed_passages(
                "http://e5.test", [f"sentence {i}" for i in range(35)]
            )

        self.assertEqual([len(batch["texts"]) for batch in seen], [32, 3])
        self.assertEqual(
            seen[0]["texts"] + seen[1]["texts"],
            [f"sentence {i}" for i in range(35)],
        )
        self.assertEqual(len(vectors), 35)
        self.assertEqual(metadata["model"], "test-model")

    def test_split_sentences_preserves_decimal_and_terminal_punctuation(self):
        self.assertEqual(
            split_sentences("O valor foi 16.5 milhões. Outra frase? Sim!"),
            ["O valor foi 16.5 milhões.", "Outra frase?", "Sim!"],
        )

    def test_archived_markdown_excludes_overlay_appendix(self):
        root = Path(__file__).resolve().parents[2]
        path = root.parent / "RenderingGen" / "crime_case_scripts_20260925" / "milton_leite_ptbr.md"
        sentences, metadata = read_markdown(path)
        self.assertEqual(metadata["scene_count"], 5)
        self.assertEqual(len(sentences), 50)
        self.assertFalse(metadata["timing_available"])
        self.assertFalse(any("Frases sobrepostas" in sentence["text"] for sentence in sentences))

    def test_align_sentence_times_handles_grouped_word_entries(self):
        sentences = [
            {"text": "Em 17 de setembro de 2026, houve 16 mandados."},
            {"text": "A investigação continua."},
        ]
        words = [
            {"text": "Em", "start_us": 100, "end_us": 200},
            {"text": "17 de setembro de 2026", "start_us": 300, "end_us": 800},
            {"text": "houve", "start_us": 900, "end_us": 1000},
            {"text": "16 mandados", "start_us": 1100, "end_us": 1300},
            {"text": "A", "start_us": 1400, "end_us": 1500},
            {"text": "investigação", "start_us": 1600, "end_us": 1800},
            {"text": "continua", "start_us": 1900, "end_us": 2200},
        ]

        align_sentence_times(sentences, words, scene_start_us=5_000_000)

        self.assertEqual((sentences[0]["start_us"], sentences[0]["end_us"]), (5_000_100, 5_001_300))
        self.assertEqual((sentences[1]["start_us"], sentences[1]["end_us"]), (5_001_400, 5_002_200))

    def test_align_sentence_times_fails_closed_for_text_mismatch(self):
        with self.assertRaisesRegex(ValueError, "cannot exactly align"):
            align_sentence_times(
                [{"text": "This narration sentence is missing."}],
                [{"text": "Different words", "start_us": 0, "end_us": 100}],
                scene_start_us=0,
            )


if __name__ == "__main__":
    unittest.main()
