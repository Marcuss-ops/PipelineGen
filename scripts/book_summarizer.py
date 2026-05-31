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
from typing import List, Optional

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

# PDF Text Extraction using PyMuPDF - returns list of page texts
def extract_pdf_pages(pdf_path) -> List[str]:
    try:
        import fitz  # PyMuPDF
    except ImportError:
        print("Missing dependency: PyMuPDF (fitz) is required for PDF text extraction.")
        print("Please install it using: pip install pymupdf")
        sys.exit(1)

    print(f"Extracting text from PDF: {pdf_path}")
    doc = fitz.open(pdf_path)
    pages = []
    
    for i, page in enumerate(doc):
        text = page.get_text().strip()
        pages.append(text)
        if (i + 1) % 50 == 0 or i + 1 == len(doc):
            print(f"  Processed page {i + 1}/{len(doc)}...")
            
    return pages

def extract_pdf_text(pdf_path):
    return "\n\n".join(extract_pdf_pages(pdf_path))

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
def call_ollama(prompt, model="gemma3:12b", system_prompt=None, host="http://127.0.0.1:11434"):
    url = f"{host.rstrip('/')}/api/generate"
    payload = {
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {
            "temperature": 0.5,
            "num_predict": 8192
        }
    }
    if system_prompt:
        payload["system"] = system_prompt
        
    req = urllib.request.Request(url, data=json.dumps(payload).encode("utf-8"), method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            res = json.loads(resp.read().decode("utf-8"))
            return res.get("response", "").strip()
    except Exception as e:
        print(f"  Warning: failed to call Ollama model '{model}': {e}")
        return ""

# Clean up function for stage directions
def clean_output(text):
    text = re.sub(r'\([^)]*\)', '', text)
    text = re.sub(r'\[[^\]]*\]', '', text)
    text = re.sub(r'\*\*[^*]*\*\*', '', text)
    text = re.sub(r'(?i)(audiobook\s+chapter\s+(begins?|ends?|starts?)),?', '', text)
    text = re.sub(r'(?i)(chapter\s+(start|end|begins?)),?', '', text)
    text = re.sub(r'#+\s*', '', text)
    text = re.sub(r'\n{3,}', '\n\n', text)
    return text.strip()

def main():
    parser = argparse.ArgumentParser(description="Book summarizer with page-based chunking.")
    parser.add_argument("--file", required=True, help="Path to PDF or EPUB book file")
    parser.add_argument("--model", default="gemma3:12b", help="Ollama model name (default: gemma3:12b)")
    parser.add_argument("--pages-per-chunk", type=int, default=4, help="Pages per summary chunk (default: 4)")
    parser.add_argument("--chunk-size", type=int, default=24000, help="Max chars per chunk for EPUB (default: 24000)")
    parser.add_argument("--ollama-url", default="http://127.0.0.1:11434", help="Ollama API endpoint URL")
    parser.add_argument("--output", default=None, help="Output summary file path (default: same dir as input)")
    args = parser.parse_args()

    book_path = Path(args.file)
    if not book_path.exists():
        print(f"Error: Book file not found at: {book_path}")
        sys.exit(1)

    ext = book_path.suffix.lower()
    
    # 1. Text Extraction - page-based for PDF, chunk-based for EPUB
    is_pdf = ext == ".pdf"
    if is_pdf:
        pages = extract_pdf_pages(str(book_path))
        total_pages = len(pages)
        print(f"Extracted {total_pages} pages from PDF.")
        
        # Group pages into chunks of N
        page_chunks = []
        for i in range(0, total_pages, args.pages_per_chunk):
            chunk_pages = pages[i:i + args.pages_per_chunk]
            chunk_text = "\n\n".join(p for p in chunk_pages if p.strip())
            page_start = i + 1
            page_end = min(i + args.pages_per_chunk, total_pages)
            if chunk_text.strip():
                page_chunks.append((page_start, page_end, chunk_text))
        
        print(f"Grouped into {len(page_chunks)} chunks of {args.pages_per_chunk} pages each.")
    else:
        if ext != ".epub":
            print(f"Unsupported file format '{ext}'. Only .pdf and .epub are supported.")
            sys.exit(1)
        raw_text = extract_epub_text(str(book_path))
        if not raw_text.strip():
            print("Error: Extracted text is empty!")
            sys.exit(1)
        chunks = chunk_text(raw_text, max_chars=args.chunk_size)
        # Adapt to same format as PDF: (start, end, text) where start/end are chunk indices
        page_chunks = [(i + 1, i + 1, c) for i, c in enumerate(chunks)]
        print(f"Split EPUB into {len(page_chunks)} chunks.")

    # 2. Summarization
    system_prompt = (
        "You are a professional audiobook narrator. Write a chapter in English based on the book section below.\n\n"
        "MANDATORY RULES:\n"
        "- Use THIRD PERSON only. Never say 'I', 'me', 'my', 'we'. Always 'Freud', 'he', 'the author'.\n"
        "- NEVER write: '(Audiobook Chapter Begins)', '(Chapter Start)', '(Music)', or any stage directions.\n"
        "- NEVER write meta-commentary like 'this section explores' or 'here's a narrative'.\n"
        "- NEVER write bullet points, lists, or markdown.\n"
        "- Explain each concept when it first appears (e.g. 'pleasure principle' → 'Freud calls the pleasure principle...').\n"
        "- End with a brief recap, varying the phrasing.\n\n"
        "BOOK SECTION:\n\n"
    )
    
    summaries = []
    null_count = 0
    
    for idx, (start, end, text) in enumerate(page_chunks):
        pages_label = f"pages {start}-{end}" if start != end else f"page {start}"
        print(f"  Summarizing chunk {idx + 1}/{len(page_chunks)} ({pages_label}, {len(text)} chars)...")
        
        prompt = f"Write an audiobook chapter in English based on this book section ({pages_label}).\n"
        prompt += "IMPORTANT: If the original text uses 'I', 'me', 'my', or 'we', rewrite in third person: 'Freud', 'he', 'the author'.\n"
        prompt += f"NO stage directions like '(Audiobook Chapter Begins)', '(Music)', '(Pause)'.\n"
        prompt += f"NO meta-commentary like 'this section explores'.\n"
        prompt += f"NO first person narration.\n"
        prompt += f"NO bullet points or markdown.\n"
        prompt += f"Explain concepts as they appear. End with a varied recap.\n\n"
        prompt += text
        summary = call_ollama(prompt, model=args.model, system_prompt=system_prompt, host=args.ollama_url)
        
        if not summary.strip():
            null_count += 1
            print(f"    -> NULL (no meaningful content)")
            continue
        else:
            summaries.append((start, end, clean_output(summary)))
            print(f"    -> OK ({len(summary)} chars)")

    print(f"\nResults: {len(summaries)} sections summarized, {null_count} null/empty sections.")

    # 3. Generate Master Summary (skip entirely - causes AI meta-commentary)
    master_summary = ""

    # 4. Save Summary
    if args.output:
        summary_path = Path(args.output)
    else:
        summary_path = book_path.parent / f"{book_path.stem}_summary.txt"
    
    with open(summary_path, "w", encoding="utf-8") as f:
        if master_summary:
            f.write(f"{clean_output(master_summary)}\n\n")
        for start, end, txt in summaries:
            f.write(f"{txt}\n\n")
        
    print(f"\nSaved summary to: {summary_path}")
    print("Done!")

if __name__ == "__main__":
    main()
