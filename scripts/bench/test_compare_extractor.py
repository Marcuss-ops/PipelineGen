import math
import unittest

from scripts.bench.compare_extractor import CASES, HEAVY_CASE_IDS, entity_metrics, gold_rows, ndcg, validate_cases
from scripts.bench.elon_phrase_impact import is_extractive


class SyntheticExtractorComparisonTests(unittest.TestCase):
    def test_fixture_uses_exact_nine_heavy_ids_and_all_cases_validate(self):
        validate_cases()
        self.assertEqual({case["id"] for case in CASES if case["heavy"]}, HEAVY_CASE_IDS)
        self.assertEqual(len(CASES), 14)
        self.assertTrue(all(case["language"] == "it" for case in CASES))

    def test_ndcg_uses_ideal_order_from_the_full_corpus(self):
        got = ndcg([2, 2, 2, 0, 0], [2, 2, 2, 2, 2, 0], 5)
        ideal = sum(3 / math.log2(index + 2) for index in range(5))
        actual = sum(3 / math.log2(index + 2) for index in range(3))
        self.assertAlmostEqual(got, actual / ideal)
        self.assertLess(got, 1.0)

    def test_s015_marks_each_repeated_tesla_span(self):
        case = next(case for case in CASES if case["id"] == "S015")
        tesla = [entity for entity in gold_rows(case) if entity["text"] == "Tesla"]
        self.assertEqual(len(tesla), 3)
        self.assertEqual([case["text"].encode()[item["start"]:item["end"]].decode() for item in tesla], ["Tesla"] * 3)

    def test_extractive_summary_accepts_source_sentences_and_rejects_inserted_or_partial_text(self):
        source = ["La frase originale contiene la negazione non avrebbe annunciato licenziamenti.", "Un'altra frase sorgente completa."]
        self.assertTrue(is_extractive(" ".join(source), source))
        self.assertFalse(is_extractive("Tesla ha annunciato licenziamenti.", source))
        self.assertFalse(is_extractive("La frase originale contiene la negazione", source))

    def test_utf8_gold_offsets_match_accented_money_and_place(self):
        case = next(case for case in CASES if case["id"] == "S011")
        raw = case["text"].encode("utf-8")
        rows = gold_rows(case)
        for surface in ("€750 milioni", "Berlino", "2 aprile 2026"):
            item = next(entity for entity in rows if entity["text"] == surface)
            self.assertEqual(raw[item["start"]:item["end"]].decode("utf-8"), surface)

    def test_visual_concept_is_counted_and_invalid_predictions_do_not_vanish(self):
        case = {"id": "c", "text": "discussed Tesla €750 milioni", "gold": [("Tesla", "ORG")], "heavy": False, "language": "it"}
        predictions = {"c": [
            {"text": "Tesla", "label": "ORG", "start": 10, "end": 15},
            {"text": "discussed Tesla", "label": "VISUAL_CONCEPT", "start": 0, "end": 15},
            {"text": "bad", "label": "ORG", "start": 10, "end": 15},
            {"text": "750", "label": "UNKNOWN", "start": 17, "end": 20},
        ]}
        result = entity_metrics([case], predictions)
        exact = result["exact_span_and_type"]
        self.assertEqual((exact["true_positive"], exact["false_positive"], exact["false_negative"]), (1, 3, 0))
        self.assertEqual(result["invalid_offsets"], 2)
        self.assertEqual(result["invalid_labels"], 1)
        self.assertEqual(result["invalid_outputs"], 2)
        self.assertEqual(result["per_label"]["CONCEPT"]["false_positive"], 1)


if __name__ == "__main__":
    unittest.main()
