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

# Google Drive upload support (auto-converts .txt to Google Docs)
_PROJECT_ROOT = Path(__file__).resolve().parent.parent
try:
    sys.path.insert(0, str(_PROJECT_ROOT / "google-accounting"))
    from drive_client import upload_file_to_drive as _drive_upload
    from config import BOOKS_DRIVE_FOLDER_ID as _books_drive_folder_id
    HAS_DRIVE = True
except ImportError as e:
    _drive_upload = None
    _books_drive_folder_id = ""
    HAS_DRIVE = False

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
def chunk_text(text, max_chars=12000):
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

def deduplicate_repetitions(text):
    """Safety net: truncates output if the same line appears >3 times (LLM repetition loop)."""
    lines = text.split('\n')
    seen = {}
    result = []
    for line in lines:
        stripped = line.strip()
        if stripped:
            seen[stripped] = seen.get(stripped, 0) + 1
            if seen[stripped] > 3:
                # Repetition loop detected - truncate here
                return '\n'.join(result)
        result.append(line)
    return '\n'.join(result)

# Local Ollama / Gemma API Summarization
def call_ollama(prompt, model="gemma3:12b", system_prompt=None, host="http://127.0.0.1:11434",
                is_instruction_mode=False):
    """Call Ollama LLM with anti-repetition safeguards."""
    url = f"{host.rstrip('/')}/api/generate"

    # Higher token budget for instruction mode (rewrite needs more space)
    max_tokens = 16384 if is_instruction_mode else 8192

    payload = {
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {
            "temperature": 0.7 if is_instruction_mode else 0.5,
            "num_predict": max_tokens,
            # Anti-repetition: penalize repeated tokens to prevent infinite loops
            "repeat_penalty": 1.15,
            # Safe sampling parameters
            "top_k": 40,
            "top_p": 0.9,
            # Stop at triple newline (safe guard against runaway generation)
            "stop": ["\n\n\n"]
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
    parser.add_argument("--chunk-size", type=int, default=12000, help="Max chars per chunk for EPUB (default: 12000)")
    parser.add_argument("--ollama-url", default="http://127.0.0.1:11434", help="Ollama API endpoint URL")
    parser.add_argument("--output", default=None, help="Output summary file path (default: same dir as input)")
    parser.add_argument("--instruction", default=None,
                        help="Custom instruction/prompt for rewriting the book. Overrides the default audiobook style. "
                             "Example: --instruction 'Rewrite this in the style of a horror story for teenagers'")
    parser.add_argument("--max-chunks", type=int, default=0,
                        help="Process only the first N chunks (default: 0 = all chunks)")
    parser.add_argument("--drive-folder-id", default=os.getenv("BOOKS_DRIVE_FOLDER_ID", _books_drive_folder_id),
                        help="Google Drive folder ID for auto-upload. If set, the .txt summary gets uploaded as a Google Doc. "
                             "Can also be set via BOOKS_DRIVE_FOLDER_ID env var or config.yaml books_root_folder.")
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
            merged_text = "\n\n".join(p for p in chunk_pages if p.strip())
            page_start = i + 1
            page_end = min(i + args.pages_per_chunk, total_pages)
            if merged_text.strip():
                page_chunks.append((page_start, page_end, merged_text))

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

    # Apply --max-chunks limit (if set, take only first N chunks)
    if args.max_chunks and args.max_chunks > 0 and len(page_chunks) > args.max_chunks:
        print(f"Limiting to first {args.max_chunks} chunks (out of {len(page_chunks)} total).")
        page_chunks = page_chunks[:args.max_chunks]

    # 2. Summarization
    user_instruction = args.instruction
    if user_instruction:
        user_instruction = user_instruction.strip()

    if user_instruction:
        # Custom rewrite mode: AI must REWRITE the content directly (no analysis/summary)
        system_prompt = (
            "You are a professional writer. Your ONLY task is to REWRITE the provided book "
            "section according to the given instruction.\n\n"
            "CRITICAL RULES - VIOLATION WILL RUIN THE OUTPUT:\n"
            "1. REWRITE the content directly - do NOT summarize, analyze, or comment on it.\n"
            "2. PRESERVE all practical advice, examples, tips, numbers, and details from the original.\n"
            "3. NEVER write sections like 'Overall Summary', 'Key Takeaways', 'Analysis', or 'Potential Improvements'.\n"
            "4. NEVER add meta-commentary like 'this section explores' or 'here's what the author suggests'.\n"
            "5. NEVER use bullet points, numbered lists, markdown headings, or bold text.\n"
            "6. Write in flowing prose/paragraphs - like a real book chapter.\n"
            "7. Match the length of the original - don't condense or shorten it.\n"
            "8. Keep all specific numbers, dollar amounts, product names, and actionable steps.\n\n"
            f"INSTRUCTION TO FOLLOW:\n{user_instruction}\n\n"
        )
    else:
        # Default audiobook narrator mode
        system_prompt = (
            "You are a professional audiobook narrator. Write a chapter in English based on the book section below.\n\n"
            "MANDATORY RULES:\n"
            "- Use THIRD PERSON only. Never say 'I', 'me', 'my', 'we'. Always 'Freud', 'he', 'the author'.\n"
            "- NEVER write: '(Audiobook Chapter Begins)', '(Chapter Start)', '(Music)', or any stage directions.\n"
            "- NEVER write meta-commentary like 'this section explores' or 'here's a narrative'.\n"
            "- NEVER write bullet points, lists, or markdown.\n"
            "- Explain each concept when it first appears (e.g. 'pleasure principle' to 'Freud calls the pleasure principle...').\n"
            "- End with a brief recap, varying the phrasing.\n\n"
            "BOOK SECTION:\n\n"
        )

    summaries = []
    null_count = 0

    for idx, (start, end, text) in enumerate(page_chunks):
        pages_label = f"pages {start}-{end}" if start != end else f"page {start}"
        print(f"  Processing chunk {idx + 1}/{len(page_chunks)} ({pages_label}, {len(text)} chars)...")

        if user_instruction:
            prompt = (
                f"REWRITE the following book section ({pages_label}). "
                f"IMPORTANT: This is a REWRITE, not a summary or analysis. "
                f"Keep all advice, examples, numbers, and details. "
                f"Change only the voice, style, and perspective to match the instruction.\n\n"
                f"INSTRUCTION: {user_instruction}\n\n"
                f"--- BOOK SECTION TO REWRITE ---\n"
                f"{text}\n"
                f"--- END OF BOOK SECTION ---\n\n"
                f"Now write the REWRITTEN version. Start directly with the rewritten content - "
                f"no headings, no introduction, no analysis. Just the rewritten text."
            )
        else:
            prompt = f"Write an audiobook chapter in English based on this book section ({pages_label}).\n"
            prompt += "IMPORTANT: If the original text uses 'I', 'me', 'my', or 'we', rewrite in third person: 'Freud', 'he', 'the author'.\n"
            prompt += f"NO stage directions like '(Audiobook Chapter Begins)', '(Music)', '(Pause)'.\n"
            prompt += f"NO meta-commentary like 'this section explores'.\n"
            prompt += f"NO first person narration.\n"
            prompt += f"NO bullet points or markdown.\n"
            prompt += f"Explain concepts as they appear. End with a varied recap.\n\n"
            prompt += text
        summary = call_ollama(prompt, model=args.model, system_prompt=system_prompt, host=args.ollama_url,
                              is_instruction_mode=bool(user_instruction))

        if user_instruction:
            summary_text = summary  # Skip clean_output for custom instructions
        else:
            summary_text = clean_output(summary)

        # Apply deduplication safety net (LLM repetition loop defense)
        summary_text = deduplicate_repetitions(summary_text)

        if not summary_text:
            null_count += 1
            print(f"    -> NULL (no meaningful content)")
            continue
        else:
            summaries.append((start, end, summary_text))
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

    # 5. Auto-upload to Google Drive as Google Doc
    drive_folder_id = args.drive_folder_id
    if drive_folder_id and HAS_DRIVE:
        try:
            doc_name = f"{book_path.stem}_rewritten.txt"
            print(f"Uploading to Google Drive folder {drive_folder_id} as Google Doc...")
            file_id = _drive_upload(
                folder_id=drive_folder_id,
                local_path=summary_path,
                filename=doc_name,
                mime_type="text/plain",
                drive_mime_type="application/vnd.google-apps.document",
            )
            if file_id:
                doc_link = f"https://docs.google.com/document/d/{file_id}/edit"
                print(f"[OK] Uploaded to Google Docs: {doc_link}")
            else:
                print("[WARN] Upload failed (no file ID returned)")
        except Exception as e:
            print(f"[WARN] Google Drive upload failed: {e}")
    elif not drive_folder_id:
        print("  (Skipped Google Drive upload: no --drive-folder-id provided)")
    elif not HAS_DRIVE:
        print("  (Skipped Google Drive upload: drive_client not available - check credentials)")

    print("Done!")

if __name__ == "__main__":
    main()
