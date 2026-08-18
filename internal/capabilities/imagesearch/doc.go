// Package imagesearch owns the deterministic Image Search Intent resolver:
// the bridge from a scene's narration text to the image search decision.
//
// Pipeline position (the certified chain):
//
//	FRASE → Semantic/Entity Extraction → Canonicalizzazione
//	      → Image Search Query → Risultati → Relevance validation
//	      → Immagine scelta
//
// The package owns the middle of that chain. It consumes the output of the
// conservative CPU extractor (or any extractor implementing the structural
// EntityExtractor port) as a BASELINE, then applies the editorial/visual
// knowledge the battery certifies:
//
//   - typed entity resolution (PERSON / ORG / PRODUCT / LANDMARK / LOCATION /
//     GPE / ANIMAL / OBJECT / CATEGORY) with a curated, conservative identity
//     knowledge base that disambiguates "Michael Jordan" from "Michael B.
//     Jordan", "Apple Inc." from "apple fruit", "Jaguar (car)" from "jaguar
//     (animal)" and pairs a brand with its product ("Tesla Cybertruck");
//   - canonicalization through the entities package (CanonicalEntityID:
//     "person:floyd-mayweather", "product:apple-vision-pro");
//   - the image search decision itself: image_search_required, the ordered
//     query list, the primary entity, negated/excluded entities, and the
//     value entities (MONEY/DATE/EVENT) that go to the VISUAL system instead
//     of a stock-image search;
//   - negation ("Tyson Fury, not Mike Tyson") and pronoun coreference
//     ("He …" → Floyd Mayweather when an antecedent is supplied);
//   - the no-image gate: an abstract sentence with no visual entity must
//     produce Required=false and never an invented entity.
//
// The resolver is pure and deterministic: the only I/O is the injected
// extractor, and the knowledge base is a closed curated table (never an
// external model), so the golden battery is a permanent regression test.
package imagesearch
