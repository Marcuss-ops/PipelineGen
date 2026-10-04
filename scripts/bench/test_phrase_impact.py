import unittest

from scripts.bench.phrase_impact import align_sentence_times, read_markdown, split_sentences
from pathlib import Path


class PhraseImpactParsingTests(unittest.TestCase):
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
