#!/usr/bin/env python3
import os
import sys
import argparse
import zipfile
import re
import urllib.request
import json
from html.parser import HTMLParser
from pathlib import Path

# Pure Python HTML Tag Stripper
class HTMLTagStripper(HTMLParser):
    def __init__(self):
        super().__init__()
        self.reset()
        self.strict = False
        self.convert_charrefs = True
        self.text = []

    def handle_data(self, d):
        self.text.append(d)

    def get_data(self):
        return ''.join(self.text)

def clean_html(html_content):
    try:
        stripper = HTMLTagStripper()
        stripper.feed(html_content)
        return stripper.get_data().strip()
    except Exception as e:
        # Fallback to regex tag stripping if HTMLParser fails
        return re.sub(r'<[^>]+>', '', html_content).strip()

# PDF Text Extraction using PyMuPDF
def extract_pdf_text(pdf_path):
    try:
        import fitz  # PyMuPDF
    except ImportError:
        print("Missing dependency: PyMuPDF (fitz) is required for PDF text extraction.")
        print("Please install it using: pip install pymupdf")
        sys.exit(1)

    print(f"Extracting text from PDF: {pdf_path}")
    doc = fitz.open(pdf_path)
    full_text = []
    
    for i, page in enumerate(doc):
        text = page.get_text()
        full_text.append(text)
        if (i + 1) % 50 == 0 or i + 1 == len(doc):
            print(f"  Processed page {i + 1}/{len(doc)}...")
            
    return "\n\n".join(full_text)

# EPUB Text Extraction using standard Zipfile and HTML parsing
def extract_epub_text(epub_path):
    print(f"Extracting text from EPUB: {epub_path}")
    if not zipfile.is_zipfile(epub_path):
        raise ValueError(f"File is not a valid EPUB zip archive: {epub_path}")
        
    full_text = []
    with zipfile.ZipFile(epub_path, 'r') as epub:
        # 1. Look for XHTML, HTML, or HTM documents
        html_files = [f for f in epub.namelist() if f.lower().endswith(('.xhtml', '.html', '.htm'))]
        
        # Sort files to try and maintain logical order
        # Simple sorting usually aligns with book chapters (e.g. chapter_1, chapter_2)
        html_files.sort()
        
        print(f"  Found {len(html_files)} document segments in EPUB.")
        for i, file_name in enumerate(html_files):
            try:
                content = epub.read(file_name).decode('utf-8', errors='ignore')
                text = clean_html(content)
                if text:
                    full_text.append(text)
            except Exception as e:
                print(f"  Warning: failed to parse EPUB segment {file_name}: {e}")
                
            if (i + 1) % 10 == 0 or i + 1 == len(html_files):
                print(f"  Processed segment {i + 1}/{len(html_files)}...")
                
    return "\n\n".join(full_text)

# Robust Chunking Strategy
def chunk_text(text, max_chars=24000):
    # Split text into paragraphs to maintain logical block separation
    paragraphs = text.split("\n")
    chunks = []
    current_chunk = []
    current_len = 0
    
    for para in paragraphs:
        para_stripped = para.strip()
        if not para_stripped:
            continue
            
        # If a single paragraph is longer than max_chars, split it by sentences
        if len(para_stripped) > max_chars:
            sentences = re.split(r'(?<=[.!?])\s+', para_stripped)
            for sentence in sentences:
                if current_len + len(sentence) + 1 > max_chars:
                    if current_chunk:
                        chunks.append("\n".join(current_chunk))
                    current_chunk = [sentence]
                    current_len = len(sentence)
                else:
                    current_chunk.append(sentence)
                    current_len += len(sentence) + 1
        else:
            if current_len + len(para_stripped) + 1 > max_chars:
                if current_chunk:
                    chunks.append("\n".join(current_chunk))
                current_chunk = [para_stripped]
                current_len = len(para_stripped)
            else:
                current_chunk.append(para_stripped)
                current_len += len(para_stripped) + 1
                
    if current_chunk:
        chunks.append("\n".join(current_chunk))
        
    return chunks

# Local Ollama / Gemma API Summarization
def call_ollama(prompt, model="gemma", system_prompt=None, host="http://127.0.0.1:11434"):
    url = f"{host.rstrip('/')}/api/generate"
    payload = {
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {
            "temperature": 0.3,
            "num_predict": 1024
        }
    }
    if system_prompt:
        payload["system"] = system_prompt
        
    req = urllib.request.Request(url, data=json.dumps(payload).encode("utf-8"), method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=180) as resp:
            res = json.loads(resp.read().decode("utf-8"))
            return res.get("response", "").strip()
    except Exception as e:
        print(f"  Warning: failed to call Ollama model '{model}': {e}")
        return ""

def main():
    parser = argparse.ArgumentParser(description="Book plain-text extractor and chunked Gemma summarizer.")
    parser.add_argument("--file", required=True, help="Path to PDF or EPUB book file")
    parser.add_argument("--model", default="gemma", help="Ollama model name (e.g. gemma, gemma2, llama3)")
    parser.add_argument("--chunk-size", type=int, default=24000, help="Maximum characters per chunk (default: 24000)")
    parser.add_argument("--ollama-url", default="http://127.0.0.1:11434", help="Ollama API endpoint URL")
    args = parser.parse_args()

    book_path = Path(args.file)
    if not book_path.exists():
        print(f"Error: Book file not found at: {book_path}")
        sys.exit(1)

    ext = book_path.suffix.lower()
    
    # 1. Text Extraction
    try:
        if ext == ".pdf":
            raw_text = extract_pdf_text(str(book_path))
        elif ext == ".epub":
            raw_text = extract_epub_text(str(book_path))
        else:
            print(f"Unsupported file format '{ext}'. Only .pdf and .epub are supported.")
            sys.exit(1)
    except Exception as e:
        print(f"Failed to extract text from book: {e}")
        sys.exit(1)

    if not raw_text.strip():
        print("Error: Extracted text is empty!")
        sys.exit(1)

    print(f"Successfully extracted {len(raw_text)} characters.")

    # Write clean plain text to output
    clean_text_path = book_path.parent / f"{book_path.stem}_clean.txt"
    with open(clean_text_path, "w", encoding="utf-8") as f:
        f.write(raw_text)
    print(f"Saved plain text output to: {clean_text_path}")

    # 2. Chunking
    chunks = chunk_text(raw_text, max_chars=args.chunk_size)
    print(f"Split book into {len(chunks)} chunks of max {args.chunk_size} characters.")

    # 3. Summarization
    print(f"Summarizing chunk-by-chunk using Ollama model '{args.model}'...")
    chunk_summaries = []
    
    system_prompt = (
        "You are an expert literary analyst. Write a concise, structured, and informative summary of the "
        "provided book section. Focus on core events, characters, themes, and key insights. Do not add intro/outro comments."
    )
    
    for idx, chunk in enumerate(chunks):
        print(f"  Summarizing chunk {idx + 1}/{len(chunks)} ({len(chunk)} characters)...")
        prompt = f"Summarize the following book section:\n\n{chunk}"
        summary = call_ollama(prompt, model=args.model, system_prompt=system_prompt, host=args.ollama_url)
        if summary:
            chunk_summaries.append(f"### SECTION {idx + 1} SUMMARY\n{summary}")
        else:
            chunk_summaries.append(f"### SECTION {idx + 1} SUMMARY\n[Failed to summarize this section]")

    # 4. Generate Final Master Summary (reduction step)
    master_summary = ""
    if len(chunk_summaries) > 1:
        print("Generating overall Master Summary from section summaries...")
        combined_summaries_text = "\n\n".join(chunk_summaries)
        master_prompt = (
            "Based on the following section summaries of a book, write a cohesive, high-level overview "
            "summarizing the entire book. Highlight the narrative arc, key character developments, and "
            "main arguments/themes:\n\n" + combined_summaries_text
        )
        master_system = "You are a senior editor. Write an elegant, comprehensive, and cohesive book summary."
        master_summary = call_ollama(master_prompt, model=args.model, system_prompt=master_system, host=args.ollama_url)

    # 5. Save Summary to File
    summary_path = book_path.parent / f"{book_path.stem}_summary.txt"
    with open(summary_path, "w", encoding="utf-8") as f:
        f.write(f"==================================================\n")
        f.write(f" BOOK SUMMARY: {book_path.stem}\n")
        f.write(f"==================================================\n\n")
        if master_summary:
            f.write(f"## OVERALL SUMMARY\n{master_summary}\n\n")
            f.write(f"==================================================\n\n")
        f.write(f"## SECTION BY SECTION SUMMARIES\n\n")
        f.write("\n\n".join(chunk_summaries))
        f.write("\n")
        
    print(f"Saved complete summarization output to: {summary_path}")
    print("Done!")

if __name__ == "__main__":
    main()
