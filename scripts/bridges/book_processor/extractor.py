import sys
import zipfile
import uuid
import re
from pathlib import Path
from typing import List, Tuple
from .utils import clean_html, chunk_text
from .config import _PROJECT_ROOT

def extract_pdf_pages(pdf_path) -> List[str]:
    try:
        import fitz
    except ImportError:
        print("Missing dependency: PyMuPDF (fitz) is required for PDF text extraction.")
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

def extract_epub_text(epub_path):
    print(f"Extracting text from EPUB: {epub_path}")
    if not zipfile.is_zipfile(epub_path):
        raise ValueError(f"File is not a valid EPUB zip archive: {epub_path}")

    full_text = []
    with zipfile.ZipFile(epub_path, 'r') as epub:
        html_files = [f for f in epub.namelist() if f.lower().endswith(('.xhtml', '.html', '.htm'))]
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

def handle_google_doc(google_doc_id, chunk_size):
    print(f"Downloading Google Docs document: {google_doc_id}")
    try:
        sys.path.insert(0, str(_PROJECT_ROOT / "google-accounting"))
        from drive_client import download_google_doc_text, get_google_doc_title
        
        doc_content = download_google_doc_text(google_doc_id)
        if not doc_content:
            print("Error: Failed to download Google Docs content")
            sys.exit(1)
        
        temp_dir = Path("/tmp")
        temp_dir.mkdir(exist_ok=True)
        temp_file = temp_dir / f"google_doc_{uuid.uuid4().hex[:8]}.txt"
        with open(temp_file, "w", encoding="utf-8") as f:
            f.write(doc_content)
        
        lines = doc_content.split("\n")
        current_chunk = []
        current_len = 0
        page_chunks = []
        
        for line in lines:
            if current_len + len(line) + 1 > chunk_size:
                if current_chunk:
                    chunk_text_str = "\n".join(current_chunk)
                    if chunk_text_str.strip():
                        page_chunks.append((len(page_chunks) + 1, len(page_chunks) + 1, chunk_text_str))
                current_chunk = [line]
                current_len = len(line)
            else:
                current_chunk.append(line)
                current_len += len(line) + 1
        
        if current_chunk:
            chunk_text_str = "\n".join(current_chunk)
            if chunk_text_str.strip():
                page_chunks.append((len(page_chunks) + 1, len(page_chunks) + 1, chunk_text_str))
        
        print(f"Downloaded {len(doc_content)} chars, split into {len(page_chunks)} chunks")
        
        google_doc_title = get_google_doc_title(google_doc_id)
        if google_doc_title:
            safe_name = re.sub(r'[^\w\s-]', '', google_doc_title).strip()[:50]
            google_doc_name = safe_name if safe_name else f"GoogleDoc_{google_doc_id[:8]}"
            print(f"  Document title: {google_doc_title}")
        else:
            google_doc_name = f"GoogleDoc_{google_doc_id[:8]}"
            
        return temp_file, page_chunks, google_doc_name
        
    except ImportError as e:
        print(f"Error: Failed to import drive_client: {e}")
        sys.exit(1)
    except Exception as e:
        print(f"Error downloading Google Docs: {e}")
        sys.exit(1)

def extract_content(book_path, ext, pages_per_chunk, chunk_size) -> Tuple[List[Tuple[int, int, str]], bool]:
    is_pre_processed_txt = False
    
    if ext == ".txt":
        print(f"Treating .txt file as already processed/rewritten content: {book_path}")
        is_pre_processed_txt = True
        with open(book_path, "r", encoding="utf-8") as f:
            raw_text = f.read()
        chunks = raw_text.split("\n\n")
        page_chunks = [(i + 1, i + 1, c.strip()) for i, c in enumerate(chunks) if c.strip()]
        print(f"Loaded {len(page_chunks)} text segments for translation/processing.")
        
    elif ext == ".pdf":
        pages = extract_pdf_pages(str(book_path))
        total_pages = len(pages)
        print(f"Extracted {total_pages} pages from PDF.")

        page_chunks = []
        for i in range(0, total_pages, pages_per_chunk):
            chunk_pages = pages[i:i + pages_per_chunk]
            merged_text = "\n\n".join(p for p in chunk_pages if p.strip())
            if merged_text.strip():
                page_chunks.append((i + 1, min(i + pages_per_chunk, total_pages), merged_text))
        print(f"Grouped into {len(page_chunks)} chunks of {pages_per_chunk} pages each.")
        
    elif ext == ".epub":
        raw_text = extract_epub_text(str(book_path))
        if not raw_text.strip():
            print("Error: Extracted text is empty!")
            sys.exit(1)
        chunks = chunk_text(raw_text, max_chars=chunk_size)
        page_chunks = [(i + 1, i + 1, c) for i, c in enumerate(chunks)]
        print(f"Split EPUB into {len(page_chunks)} chunks.")
    else:
        print(f"Unsupported file format '{ext}'. Only .pdf, .epub and .txt are supported.")
        sys.exit(1)
        
    return page_chunks, is_pre_processed_txt