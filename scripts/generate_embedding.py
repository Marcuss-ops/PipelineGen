#!/usr/bin/env python3
import sys
import json
import argparse
from sentence_transformers import SentenceTransformer

def main():
    parser = argparse.ArgumentParser(description="Generate text embedding for Hybrid Search")
    parser.add_argument("--text", type=str, required=True, help="Text to embed")
    args = parser.parse_args()

    # Usiamo un modello piccolo, veloce e multilingua per fare match it/en
    model_name = 'sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2'
    
    try:
        model = SentenceTransformer(model_name)
        # Normalize_embeddings=True è FONDAMENTALE per usare il DotProduct come Cosine Similarity in Go
        embedding = model.encode(args.text, normalize_embeddings=True)
        
        # Stampa l'array JSON su stdout
        print(json.dumps(embedding.tolist()))
    except Exception as e:
        print(json.dumps({"error": str(e)}), file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
