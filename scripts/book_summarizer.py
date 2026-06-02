#!/usr/bin/env python3
import os
import sys
import argparse
import zipfile
import re
import urllib.request
import json
import uuid
from html.parser import HTMLParser
from pathlib import Path
from typing import List, Optional, Tuple
from datetime import datetime

# Google Drive upload support (auto-converts .txt to Google Docs)
_PROJECT_ROOT = Path(__file__).resolve().parent.parent
try:
    sys.path.insert(0, str(_PROJECT_ROOT / "google-accounting"))
    from drive_client import upload_file_to_drive as _drive_upload, _build_service
    from config import BOOKS_DRIVE_FOLDER_ID as _books_drive_folder_id
    HAS_DRIVE = True
except ImportError as e:
    _drive_upload = None
    _build_service = None
    _books_drive_folder_id = ""
    HAS_DRIVE = False

# PDF generation support
try:
    from reportlab.lib.pagesizes import letter, A4
    from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
    from reportlab.lib.units import inch
    from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, PageBreak
    from reportlab.lib.enums import TA_LEFT, TA_CENTER, TA_JUSTIFY
    HAS_REPORTLAB = True
except ImportError:
    HAS_REPORTLAB = False

# Supported languages for translation
LANGUAGES = {
    "en": "English",
    "es": "Spanish",
    "fr": "French",
    "de": "German",
    "it": "Italian",
    "pt": "Portuguese",
    "pl": "Polish",
    "nl": "Dutch",
    "ja": "Japanese",
    "ko": "Korean",
    "ru": "Russian",
    "tr": "Turkish",
    "id": "Indonesian",
    "zh": "Chinese",
    "ar": "Arabic",
    "hi": "Hindi",
}

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
                return '\n'.join(result)
        result.append(line)
    return '\n'.join(result)


def call_ollama(prompt, model="gemma3:12b", system_prompt=None, host="http://127.0.0.1:11434",
                is_instruction_mode=False):
    url = f"{host.rstrip('/')}/api/generate"
    payload = {
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {
            "temperature": 0.3,
            "num_predict": 4096,
            "repeat_penalty": 1.3,
            "top_k": 40,
            "top_p": 0.9,
            "stop": ["\n\n\n"],
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


# Translation function using Ollama with translategemma or gemma3
def translate_text_ollama(text: str, target_language: str, model: str = "gemma3:12b", host: str = "http://127.0.0.1:11434") -> str:
    """Translate text using Ollama with translategemma or gemma fallback."""
    lang_name = LANGUAGES.get(target_language.lower(), target_language)
    prompt = (
        f"You are a professional translator. Translate the following text to {lang_name}. "
        "Preserve the style, tone, and formatting. Return ONLY the translated text. "
        "Do NOT repeat sentences or paragraphs. Do NOT add explanations.\n\n"
        f"Text to translate:\n{text}"
    )
    translated = call_ollama(prompt, model=model, host=host, is_instruction_mode=False)
    if not translated:
        print(f"    Translation with {model} failed, trying translategemma:4b fallback...")
        translated = call_ollama(prompt, model="translategemma:4b", host=host, is_instruction_mode=False)
    return translated or ""

# Generate PDF from text
def generate_pdf(text_path: Path, output_path: Path, title: str = "") -> bool:
    """Generate a PDF document from a text file using ReportLab."""
    if not HAS_REPORTLAB:
        print("  reportlab not installed, skipping PDF generation. Install with: pip install reportlab")
        return False
    
    try:
        # Read the text content
        with open(text_path, "r", encoding="utf-8") as f:
            content = f.read()
        
        # Create PDF document
        doc = SimpleDocTemplate(str(output_path), pagesize=A4,
                                rightMargin=72, leftMargin=72,
                                topMargin=72, bottomMargin=72)
        
        # Build content
        story = []
        styles = getSampleStyleSheet()
        
        # Title style
        title_style = ParagraphStyle(
            'CustomTitle',
            parent=styles['Heading1'],
            fontSize=24,
            leading=30,
            alignment=TA_CENTER,
            spaceAfter=30,
        )
        
        # Body text style
        body_style = ParagraphStyle(
            'CustomBody',
            parent=styles['Normal'],
            fontSize=11,
            leading=16,
            alignment=TA_JUSTIFY,
            spaceAfter=12,
        )
        
        # Add title if provided
        if title:
            story.append(Paragraph(title, title_style))
            story.append(Spacer(1, 20))
        
        # Split content into paragraphs and add to story
        paragraphs = content.split("\n\n")
        for para in paragraphs:
            para = para.strip()
            if not para:
                continue
            # Check if it's a heading (short line without ending punctuation)
            if len(para) < 100 and not para[-1] in '.!?':
                heading_style = ParagraphStyle(
                    'Heading',
                    parent=styles['Heading2'],
                    fontSize=14,
                    leading=18,
                    spaceBefore=20,
                    spaceAfter=10,
                )
                story.append(Paragraph(para, heading_style))
            else:
                story.append(Paragraph(para, body_style))
            story.append(Spacer(1, 6))
        
        # Build PDF
        doc.build(story)
        print(f"  Generated PDF: {output_path}")
        return True
        
    except Exception as e:
        print(f"  PDF generation error: {e}")
        return False

# Create structured folder on Google Drive for the book
def create_book_drive_folder(book_name: str, drive_folder_id: str, language: str = "en") -> Tuple[str, str]:
    """Create a structured folder on Google Drive for the book with language subfolder."""
    if not HAS_DRIVE or not _build_service:
        return "", ""
    
    try:
        service = _build_service()
        
        # Create main book folder
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        book_folder_name = f"{book_name[:50]}"
        
        # Create book folder
        book_folder = {
            "name": book_folder_name,
            "mimeType": "application/vnd.google-apps.folder",
            "parents": [drive_folder_id] if drive_folder_id else []
        }
        
        created_book_folder = service.files().create(body=book_folder, fields="id, name").execute()
        book_folder_id = created_book_folder.get("id")
        print(f"  Created Drive folder: {book_folder_name} ({book_folder_id})")
        
        # Create language subfolder
        lang_name = LANGUAGES.get(language.lower(), language)
        lang_folder_name = f"{lang_name}"
        
        lang_folder = {
            "name": lang_folder_name,
            "mimeType": "application/vnd.google-apps.folder",
            "parents": [book_folder_id]
        }
        
        created_lang_folder = service.files().create(body=lang_folder, fields="id, name").execute()
        lang_folder_id = created_lang_folder.get("id")
        print(f"  Created language subfolder: {lang_folder_name} ({lang_folder_id})")
        
        # Create PDFs subfolder
        pdf_folder = {
            "name": "PDF",
            "mimeType": "application/vnd.google-apps.folder",
            "parents": [lang_folder_id]
        }
        created_pdf_folder = service.files().create(body=pdf_folder, fields="id, name").execute()
        pdf_folder_id = created_pdf_folder.get("id")
        print(f"  Created PDF subfolder ({pdf_folder_id})")
        
        return book_folder_id, pdf_folder_id
        
    except Exception as e:
        print(f"  Drive folder creation error: {e}")
        return "", ""

# Upload multiple files to Drive with structured folders
def upload_book_to_drive(summary_path: Path, pdf_path: Path, book_name: str, 
                         drive_folder_id: str, language: str = "en") -> dict:
    """Upload book files to Google Drive with proper folder structure."""
    if not HAS_DRIVE:
        return {"success": False, "error": "Drive client not available"}
    
    try:
        # Create folder structure
        book_folder_id, pdf_folder_id = create_book_drive_folder(book_name, drive_folder_id, language)
        
        if not book_folder_id:
            # Fallback: upload directly to root folder
            book_folder_id = drive_folder_id
            pdf_folder_id = drive_folder_id
        
        results = {"success": True, "folders": {}, "files": {}}
        
        # Upload text file as Google Doc
        if summary_path.exists():
            doc_name = f"{book_name}_rewritten.txt"
            lang_name = LANGUAGES.get(language.lower(), language)
            
            file_id = _drive_upload(
                folder_id=book_folder_id,
                local_path=summary_path,
                filename=doc_name,
                mime_type="text/plain",
                drive_mime_type="application/vnd.google-apps.document",
            )
            if file_id:
                doc_link = f"https://docs.google.com/document/d/{file_id}/edit"
                print(f"  [OK] Uploaded text as Google Doc: {doc_link}")
                results["files"]["document"] = {"id": file_id, "link": doc_link}
        
        # Upload PDF
        if pdf_path and Path(pdf_path).exists():
            pdf_name = f"{book_name}.pdf"
            pdf_folder = pdf_folder_id if pdf_folder_id else book_folder_id
            
            pdf_id = _drive_upload(
                folder_id=pdf_folder,
                local_path=pdf_path,
                filename=pdf_name,
                mime_type="application/pdf",
            )
            if pdf_id:
                pdf_link = f"https://drive.google.com/file/d/{pdf_id}/view"
                print(f"  [OK] Uploaded PDF: {pdf_link}")
                results["files"]["pdf"] = {"id": pdf_id, "link": pdf_link}
        
        # Store folder info
        if book_folder_id:
            results["folders"]["book"] = {
                "id": book_folder_id,
                "link": f"https://drive.google.com/drive/folders/{book_folder_id}"
            }
        
        return results
        
    except Exception as e:
        print(f"  Drive upload error: {e}")
        return {"success": False, "error": str(e)}

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
    parser = argparse.ArgumentParser(description="Book summarizer with page-based chunking, translation, and PDF generation.")
    parser.add_argument("--file", help="Path to PDF or EPUB book file")
    parser.add_argument("--google-doc-id", help="Google Docs document ID to download and process")
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
                        help="Google Drive folder ID for auto-upload. "
                             "Can also be set via BOOKS_DRIVE_FOLDER_ID env var or config.yaml books_root_folder.")
    parser.add_argument("--language", "--target-language", dest="language", default=None,
                        help=f"Target language for translation. Supported: {', '.join(LANGUAGES.keys())}. "
                             "If not set, output is in English.")
    parser.add_argument("--translate-only", action="store_true",
                        help="Skip rewriting, only translate the original text chunks to target language.")
    parser.add_argument("--generate-pdf", action="store_true",
                        help="Generate PDF version in addition to text output.")
    args = parser.parse_args()

    # Validate translate-only mode
    if args.translate_only and not args.language:
        print("Error: --translate-only requires --language to be specified")
        print(f"Supported languages: {', '.join(LANGUAGES.keys())}")
        sys.exit(1)
    if args.translate_only and args.language and args.language.lower() not in LANGUAGES:
        print(f"Error: Unsupported language '{args.language}'")
        print(f"Supported languages: {', '.join(LANGUAGES.keys())}")
        sys.exit(1)
    if args.translate_only and args.instruction:
        print("Note: --instruction is ignored in translate-only mode (only translation, no rewrite)")

    # Validate input source
    if not args.file and not args.google_doc_id:
        print("Error: Either --file or --google-doc-id must be specified")
        sys.exit(1)
    if args.file and args.google_doc_id:
        print("Error: Cannot specify both --file and --google-doc-id. Use one or the other.")
        sys.exit(1)

    # Handle Google Docs source
    google_doc_id = None
    google_doc_name = None
    google_doc_title = None
    if args.google_doc_id:
        print(f"Downloading Google Docs document: {args.google_doc_id}")
        
        # Import and use drive_client to download
        try:
            sys.path.insert(0, str(_PROJECT_ROOT / "google-accounting"))
            from drive_client import download_google_doc_text, get_google_doc_title
            doc_content = download_google_doc_text(args.google_doc_id)
            if not doc_content:
                print("Error: Failed to download Google Docs content")
                sys.exit(1)
            
            # Save to temp file for processing
            temp_dir = Path("/tmp")
            temp_dir.mkdir(exist_ok=True)
            temp_file = temp_dir / f"google_doc_{uuid.uuid4().hex[:8]}.txt"
            with open(temp_file, "w", encoding="utf-8") as f:
                f.write(doc_content)
            
            book_path = temp_file
            
            # Split content into chunks for processing
            # Use the text directly as chunks since there's no page structure
            lines = doc_content.split("\n")
            current_chunk = []
            current_len = 0
            max_chars = args.chunk_size
            page_chunks = []
            
            for line in lines:
                if current_len + len(line) + 1 > max_chars:
                    if current_chunk:
                        chunk_text = "\n".join(current_chunk)
                        if chunk_text.strip():
                            page_chunks.append((len(page_chunks) + 1, len(page_chunks) + 1, chunk_text))
                    current_chunk = [line]
                    current_len = len(line)
                else:
                    current_chunk.append(line)
                    current_len += len(line) + 1
            
            if current_chunk:
                chunk_text = "\n".join(current_chunk)
                if chunk_text.strip():
                    page_chunks.append((len(page_chunks) + 1, len(page_chunks) + 1, chunk_text))
            
            print(f"Downloaded {len(doc_content)} chars, split into {len(page_chunks)} chunks")
            
            # Track that we downloaded from Google Docs for output naming
            google_doc_id = args.google_doc_id
            # Get the document title for use in folder naming
            google_doc_title = get_google_doc_title(google_doc_id)
            if google_doc_title:
                # Sanitize title for use as folder name
                safe_name = re.sub(r'[^\w\s-]', '', google_doc_title).strip()[:50]
                google_doc_name = safe_name if safe_name else f"GoogleDoc_{google_doc_id[:8]}"
                print(f"  Document title: {google_doc_title}")
            else:
                google_doc_name = f"GoogleDoc_{google_doc_id[:8]}"
            
        except ImportError as e:
            print(f"Error: Failed to import drive_client: {e}")
            print("Make sure google-accounting is accessible")
            sys.exit(1)
        except Exception as e:
            print(f"Error downloading Google Docs: {e}")
            sys.exit(1)
    else:
        book_path = Path(args.file)
        if not book_path.exists():
            print(f"Error: Book file not found at: {book_path}")
            sys.exit(1)

        ext = book_path.suffix.lower()

    # 1. Text Extraction - page-based for PDF, chunk-based for EPUB, direct for Google Docs
    if google_doc_id:
        # page_chunks already set from Google Docs download above
        pass
    elif ext == ".pdf":
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
    target_language = args.language
    
    # 3b. Translate-only mode: skip rewriting and translate raw extracted text
    if args.translate_only and target_language and target_language.lower() in LANGUAGES:
        print(f"\nTRANSLATE-ONLY MODE: Skipping rewrite, translating raw text directly...")
        
        # Use the original page_chunks (raw text) for translation
        raw_chunks_to_translate = page_chunks  # Already limited by max_chunks above
        
        # Translate each raw chunk directly without rewriting
        translated_chunks = []
        for idx, (start, end, text) in enumerate(raw_chunks_to_translate):
            pages_label = f"pages {start}-{end}" if start != end else f"page {start}"
            print(f"  Translating chunk {idx + 1}/{len(raw_chunks_to_translate)} ({pages_label}, {len(text)} chars)...")
            translated = translate_text_ollama(text, target_language, model=args.model, host=args.ollama_url)
            if translated:
                translated = deduplicate_repetitions(translated)
                translated_chunks.append((start, end, translated))
                print(f"    -> Translated ({len(translated)} chars)")
            else:
                translated = deduplicate_repetitions(text)
                translated_chunks.append((start, end, translated))
                print(f"    -> Translation failed, keeping original")
        
        summaries = translated_chunks
        print(f"  Translate-only complete: {len(summaries)} chunks translated")
    else:
        # Normal rewrite/summarize flow
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

        # 3. Translation (if requested) - only for normal rewrite mode
        if target_language and target_language.lower() in LANGUAGES:
            lang_name = LANGUAGES[target_language.lower()]
            print(f"\nSTEP 3: Translating output to {lang_name}...")
            
            translated_summaries = []
            for idx, (start, end, txt) in enumerate(summaries):
                print(f"  Translating section {idx + 1}/{len(summaries)}...")
                translated = translate_text_ollama(txt, target_language, model=args.model, host=args.ollama_url)
                if translated:
                    translated_summaries.append((start, end, translated))
                    print(f"    -> Translated ({len(translated)} chars)")
                else:
                    # Fallback to original if translation fails
                    translated_summaries.append((start, end, txt))
                    print(f"    -> Translation failed, using original")
            
            summaries = translated_summaries
            print(f"  Translation complete: {len(summaries)} sections translated")

    # 4. Save Summary
    if args.output:
        summary_path = Path(args.output)
    else:
        suffix = f"_{target_language}" if target_language else ""
        summary_path = book_path.parent / f"{book_path.stem}{suffix}_summary.txt"

    with open(summary_path, "w", encoding="utf-8") as f:
        for start, end, txt in summaries:
            f.write(f"{txt}\n\n")

    print(f"\nSaved summary to: {summary_path}")

    # 5. Generate PDF (if requested)
    pdf_path = None
    if args.generate_pdf:
        if not HAS_REPORTLAB:
            print(f"\n  reportlab not installed. Installing: pip install reportlab")
        else:
            print(f"\nSTEP 5: Generating PDF...")
            suffix = f"_{target_language}" if target_language else ""
            pdf_path = summary_path.with_suffix(".pdf")
            title = f"{book_path.stem}{suffix}"
            if generate_pdf(summary_path, pdf_path, title):
                print(f"  PDF generated: {pdf_path}")
            else:
                pdf_path = None

    # 6. Auto-upload to Google Drive with structured folders
    drive_folder_id = args.drive_folder_id
    if drive_folder_id and HAS_DRIVE:
        print(f"\nSTEP 6: Uploading to Google Drive...")
        # Use Google Doc title if available, otherwise fall back to book_path stem
        book_name = google_doc_name if google_doc_name else book_path.stem
        language = target_language if target_language else "en"
        
        upload_result = upload_book_to_drive(
            summary_path=summary_path,
            pdf_path=pdf_path,
            book_name=book_name,
            drive_folder_id=drive_folder_id,
            language=language
        )
        
        if upload_result.get("success"):
            # Print final result summary
            print(f"\n  Drive upload complete!")
            if "folders" in upload_result and "book" in upload_result["folders"]:
                print(f"  Book folder: {upload_result['folders']['book']['link']}")
            for file_type, file_info in upload_result.get("files", {}).items():
                print(f"  {file_type.upper()}: {file_info.get('link', 'N/A')}")
        else:
            print(f"  Drive upload failed: {upload_result.get('error', 'Unknown error')}")
    elif not drive_folder_id:
        print(f"\n  (Skipped Google Drive upload: no --drive-folder-id provided)")
    elif not HAS_DRIVE:
        print(f"\n  (Skipped Google Drive upload: drive_client not available - check credentials)")

    # 7. Output JSON result for Go integration
    result = {
        "success": True,
        "input_file": str(book_path),
        "output_file": str(summary_path),
        "pdf_file": str(pdf_path) if pdf_path else None,
        "language": target_language if target_language else "en",
        "chunks_processed": len(summaries),
        "null_chunks": null_count,
    }
    
    # Add drive info if available
    if drive_folder_id and HAS_DRIVE and upload_result.get("success"):
        result["drive"] = {
            "folder": upload_result.get("folders", {}).get("book", {}).get("link", ""),
            "document": upload_result.get("files", {}).get("document", {}).get("link", ""),
            "pdf": upload_result.get("files", {}).get("pdf", {}).get("link", ""),
        }
    
    # Cleanup temp file if we downloaded from Google Docs
    if google_doc_id and temp_file and temp_file.exists():
        try:
            temp_file.unlink()
            print(f"\n  Cleaned up temp file: {temp_file}")
        except Exception as e:
            print(f"\n  Warning: Could not clean up temp file {temp_file}: {e}")

    print(f"\n[RESULT]" + json.dumps(result))
    print("Done!")

if __name__ == "__main__":
    main()
