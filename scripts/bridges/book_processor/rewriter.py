from .llm import call_ollama
from .utils import clean_output, deduplicate_repetitions

def rewrite_chunks(page_chunks, user_instruction, args):
    if user_instruction:
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
        system_prompt = (
            "You are a professional writer tasked with rewriting a section of a book.\n\n"
            "MANDATORY RULES:\n"
            "- REWRITE the content naturally as if YOU are the original writer. Do NOT act as a summarizer or commentator.\n"
            "- NEVER use the phrases 'the author', 'the book', 'this section', 'the writer', or 'he/she explains/suggests'.\n"
            "- If the original text uses 'I', 'me', 'my', rewrite it in the third person, but describe the actions or events directly without referencing the original narrator.\n"
            "- Explain concepts directly as they appear.\n"
            "- NEVER write: '(Audiobook Chapter Begins)', '(Chapter Start)', '(Music)', or any stage directions.\n"
            "- NEVER write bullet points, lists, or markdown.\n\n"
            "BOOK SECTION:\n\n"
        )

    summaries = []
    null_count = 0
    
    for idx, (start, end, text) in enumerate(page_chunks):
        pages_label = f"pages {start}-{end}" if start != end else f"page {start}"
        progress_pct = int((idx + 1) / len(page_chunks) * 70) + 10  # 10-80 range
        print(f"[PROGRESS] {progress_pct}% Processing chunk {idx + 1}/{len(page_chunks)} ({pages_label}, {len(text)} chars)")

        # Extract previous chunk ending as context if overlap is configured
        prev_context = ""
        overlap_size = getattr(args, 'overlap_size', 0) if args else 0
        if idx > 0 and overlap_size > 0:
            prev_chunk_text = page_chunks[idx - 1][2]
            # Take last characters up to overlap_size
            chunk_context = prev_chunk_text[-overlap_size:]
            # Avoid cutting in the middle of a word/sentence where possible
            newline_idx = chunk_context.find('\n')
            if newline_idx != -1:
                chunk_context = chunk_context[newline_idx:].strip()
            if chunk_context:
                prev_context = (
                    "CONTEXT FROM PREVIOUS SECTION (for continuity only, DO NOT rewrite or summarize this part):\n"
                    f"... {chunk_context}\n"
                    "--- END OF PREVIOUS CONTEXT ---\n\n"
                )

        if user_instruction:
            prompt = (
                f"{prev_context}"
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
            prompt = f"{prev_context}"
            prompt += f"Rewrite this book section natively as if it were the original text ({pages_label}).\n"
            prompt += "IMPORTANT: Write directly about the subject matter. NEVER say 'The author states' or 'He explains'.\n"
            prompt += "If the original text uses 'I', 'me', 'my', rewrite in the third person directly describing the actions/events.\n"
            prompt += f"NO stage directions like '(Audiobook Chapter Begins)'.\n"
            prompt += f"NO meta-commentary.\n"
            prompt += f"NO bullet points or markdown.\n\n"
            prompt += text
            
        summary = call_ollama(prompt, model=args.model, system_prompt=system_prompt, host=args.ollama_url,
                              is_instruction_mode=bool(user_instruction))

        if user_instruction:
            summary_text = summary
        else:
            summary_text = clean_output(summary)

        summary_text = deduplicate_repetitions(summary_text)

        if not summary_text:
            null_count += 1
            print(f"    -> NULL (no meaningful content)")
        else:
            summaries.append((start, end, summary_text))
            print(f"    -> OK ({len(summary)} chars)")

    print(f"\nResults: {len(summaries)} sections summarized, {null_count} null/empty sections.")
    return summaries, null_count