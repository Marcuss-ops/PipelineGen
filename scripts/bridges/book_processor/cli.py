import argparse
import sys
import re
import json
from pathlib import Path
import os

from .config import LANGUAGES, BOOKS_DRIVE_FOLDER_ID
from .extractor import extract_content, handle_google_doc
from .rewriter import rewrite_chunks
from .llm import translate_text_ollama
from .pdf_gen import generate_pdf
from .drive import upload_book_to_drive, HAS_DRIVE

def check_dependencies():
    import subprocess
    import sys
    deps = {
        "fitz": "PyMuPDF",
        "reportlab": "reportlab"
    }
    for mod_name, pkg_name in deps.items():
        try:
            __import__(mod_name)
        except ImportError:
            print(f"Dependency '{pkg_name}' is missing. Attempting automatic installation...")
            try:
                subprocess.check_call([sys.executable, "-m", "pip", "install", pkg_name])
                print(f"Successfully installed '{pkg_name}'!")
            except Exception as e:
                print(f"Warning: Failed to auto-install '{pkg_name}': {e}")
                print(f"Please install it manually: pip install {pkg_name}")

def main():
    check_dependencies()
    parser = argparse.ArgumentParser(description="Book summarizer with page-based chunking, translation, and PDF generation.")
    parser.add_argument("--file", help="Path to PDF, EPUB, or TXT book file. If TXT, rewriting is skipped.")
    parser.add_argument("--google-doc-id", help="Google Docs document ID to download and process")
    parser.add_argument("--model", default="gemma4:e4b", help="Ollama model name (default: gemma4:e4b)")
    parser.add_argument("--fallback-model", default="translategemma:4b", help="Ollama fallback model name for translation (default: translategemma:4b)")
    parser.add_argument("--pages-per-chunk", type=int, default=4, help="Pages per summary chunk (default: 4)")
    parser.add_argument("--chunk-size", type=int, default=12000, help="Max chars per chunk for EPUB/TXT (default: 12000)")
    parser.add_argument("--overlap-size", type=int, default=1000, help="Characters of overlap/context from the previous chunk (default: 1000, 0 to disable)")
    parser.add_argument("--ollama-url", default="http://127.0.0.1:11434", help="Ollama API endpoint URL")
    parser.add_argument("--output", default=None, help="Output summary file path (default: same dir as input)")
    parser.add_argument("--instruction", default=None,
                        help="Custom instruction/prompt for rewriting the book. Overrides the default style.")
    parser.add_argument("--max-chunks", type=int, default=0,
                        help="Process only the first N chunks (default: 0 = all chunks)")
    parser.add_argument("--drive-folder-id", default=BOOKS_DRIVE_FOLDER_ID,
                        help="Google Drive folder ID for auto-upload.")
    parser.add_argument("--language", "--target-language", dest="language", default=None,
                        help=f"Target language for translation. Supported: {', '.join(LANGUAGES.keys())}.")
    parser.add_argument("--translate-only", action="store_true",
                        help="Skip rewriting, only translate the original text chunks to target language.")
    parser.add_argument("--generate-pdf", action="store_true",
                        help="Generate PDF version in addition to text output.")
    parser.add_argument("--pdf-style", default="modern", choices=["default", "classic", "modern", "academic", "colorful"],
                        help="Style theme for the generated PDF (default: modern)")
    args = parser.parse_args()

    if args.translate_only and not args.language:
        print("Error: --translate-only requires --language to be specified")
        sys.exit(1)
    if args.language and args.language.lower() not in LANGUAGES:
        print(f"Error: Unsupported language '{args.language}'")
        sys.exit(1)

    if not args.file and not args.google_doc_id:
        print("Error: Either --file or --google-doc-id must be specified")
        sys.exit(1)
    if args.file and args.google_doc_id:
        print("Error: Cannot specify both --file and --google-doc-id.")
        sys.exit(1)

    google_doc_id = args.google_doc_id
    google_doc_name = None
    temp_file = None
    is_pre_processed_txt = False

    if google_doc_id:
        temp_file, page_chunks, google_doc_name = handle_google_doc(google_doc_id, args.chunk_size)
        book_path = temp_file
    else:
        book_path = Path(args.file)
        if not book_path.exists():
            print(f"Error: Book file not found at: {book_path}")
            sys.exit(1)
        ext = book_path.suffix.lower()
        page_chunks, is_pre_processed_txt = extract_content(book_path, ext, args.pages_per_chunk, args.chunk_size)

    if args.max_chunks and args.max_chunks > 0 and len(page_chunks) > args.max_chunks:
        print(f"Limiting to first {args.max_chunks} chunks (out of {len(page_chunks)} total).")
        page_chunks = page_chunks[:args.max_chunks]

    target_language = args.language
    summaries = []
    null_count = 0

    # Determine if we should skip rewriting
    skip_rewrite = args.translate_only or is_pre_processed_txt

    if skip_rewrite:
        print(f"\nSKIPPING REWRITE: Translating direct text to target language...")
        if not target_language:
            # If no target language, the "summary" is just the raw text
            summaries = page_chunks
        else:
            for idx, (start, end, text) in enumerate(page_chunks):
                pages_label = f"pages {start}-{end}" if start != end else f"page {start}"
                progress_pct = int((idx + 1) / len(page_chunks) * 70) + 10
                print(f"[PROGRESS] {progress_pct}% Translating chunk {idx + 1}/{len(page_chunks)} ({pages_label}, {len(text)} chars)")
                translated = translate_text_ollama(text, target_language, model=args.model, fallback_model=args.fallback_model, host=args.ollama_url)
                if translated:
                    summaries.append((start, end, translated))
                    print(f"    -> Translated ({len(translated)} chars)")
                else:
                    summaries.append((start, end, text))
                    print(f"    -> Translation failed, keeping original")
    else:
        # Normal rewrite/summarize flow
        summaries, null_count = rewrite_chunks(page_chunks, args.instruction, args)

        if target_language and target_language.lower() in LANGUAGES:
            lang_name = LANGUAGES[target_language.lower()]
            print(f"\nSTEP 3: Translating output to {lang_name}...")
            translated_summaries = []
            for idx, (start, end, txt) in enumerate(summaries):
                progress_pct = int((idx + 1) / len(summaries) * 15) + 80  # 80-95 range
                print(f"[PROGRESS] {progress_pct}% Translating section {idx + 1}/{len(summaries)}")
                translated = translate_text_ollama(txt, target_language, model=args.model, fallback_model=args.fallback_model, host=args.ollama_url)
                if translated:
                    translated_summaries.append((start, end, translated))
                    print(f"    -> Translated ({len(translated)} chars)")
                else:
                    translated_summaries.append((start, end, txt))
                    print(f"    -> Translation failed, using original")
            summaries = translated_summaries
            print(f"  Translation complete: {len(summaries)} sections translated")

    # Determine Title and Translate it
    base_book_name = google_doc_name if google_doc_name else book_path.stem
    final_book_name = base_book_name
    
    if target_language and target_language.lower() in LANGUAGES and target_language.lower() != "en":
        print(f"\n  Translating book title '{base_book_name}' to {LANGUAGES[target_language.lower()]}...")
        translated_title = translate_text_ollama(base_book_name, target_language, model=args.model, fallback_model=args.fallback_model, host=args.ollama_url)
        if translated_title:
            translated_title = translated_title.strip('*_"\' ')
            print(f"    -> Translated title: {translated_title}")
            final_book_name = translated_title

    # Save Summary
    if args.output:
        summary_path = Path(args.output)
    else:
        safe_filename = re.sub(r'[^\w\s-]', '', final_book_name).strip().replace(' ', '_')
        suffix = f"_{target_language}" if target_language else ""
        summary_path = book_path.parent / f"{safe_filename}{suffix}_summary.txt"

    with open(summary_path, "w", encoding="utf-8") as f:
        for start, end, txt in summaries:
            f.write(f"{txt}\n\n")

    print(f"\nSaved summary to: {summary_path}")

    # Generate PDF
    pdf_path = None
    if args.generate_pdf:
        print(f"\nSTEP 5: Generating PDF...")
        pdf_path = summary_path.with_suffix(".pdf")
        if generate_pdf(summary_path, pdf_path, final_book_name, style_name=args.pdf_style):
            print(f"  PDF generated: {pdf_path}")
        else:
            pdf_path = None

    # Auto-upload to Google Drive
    drive_folder_id = args.drive_folder_id
    upload_result = {}
    if drive_folder_id and HAS_DRIVE:
        print(f"\nSTEP 6: Uploading to Google Drive...")
        language = target_language if target_language else "en"
        upload_result = upload_book_to_drive(
            summary_path=summary_path,
            pdf_path=pdf_path,
            book_name=final_book_name,
            drive_folder_id=drive_folder_id,
            language=language
        )
        if upload_result.get("success"):
            print(f"\n  Drive upload complete!")
            if "folders" in upload_result and "book" in upload_result["folders"]:
                print(f"  Book folder: {upload_result['folders']['book']['link']}")
            for file_type, file_info in upload_result.get("files", {}).items():
                print(f"  {file_type.upper()}: {file_info.get('link', 'N/A')}")
        else:
            print(f"  Drive upload failed: {upload_result.get('error', 'Unknown error')}")
    elif not drive_folder_id:
        print(f"\n  (Skipped Google Drive upload: no --drive-folder-id provided)")

    # Output JSON result
    result = {
        "success": True,
        "input_file": str(book_path),
        "output_file": str(summary_path),
        "pdf_file": str(pdf_path) if pdf_path else None,
        "language": target_language if target_language else "en",
        "chunks_processed": len(summaries),
        "null_chunks": null_count,
    }
    
    if drive_folder_id and HAS_DRIVE and upload_result.get("success"):
        result["drive"] = {
            "folder": upload_result.get("folders", {}).get("book", {}).get("link", ""),
            "document": upload_result.get("files", {}).get("document", {}).get("link", ""),
            "pdf": upload_result.get("files", {}).get("pdf", {}).get("link", ""),
        }
    
    if temp_file and temp_file.exists():
        try:
            temp_file.unlink()
            print(f"\n  Cleaned up temp file: {temp_file}")
        except Exception as e:
            pass

    print(f"\n[RESULT]" + json.dumps(result))
    print("Done!")

if __name__ == "__main__":
    main()