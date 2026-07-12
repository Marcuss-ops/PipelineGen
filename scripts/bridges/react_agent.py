#!/usr/bin/env python3
"""ReAct (Reason + Act) agent bridge for the script-docs pipeline.

Protocol:
  - Receives a JSON payload via --json CLI arg:
    {"topic": "...", "context": "...", "max_steps": N, "ollama_url": "...", "ollama_model": "..."}
  - Runs a Think → Act → Observe loop (max N steps) via Ollama /api/chat
  - Outputs structured JSON to stdout:
    {"result": "...", "status": "ok", "steps_taken": N, "evidence": [...]}
  - Exit 0 on success, non-zero on failure (error message to stderr)

godlike/07 NO-FAKE-AVAILABILITY: every failure exits non-zero with a
diagnostic message on stderr so the Go adapter can surface it as a typed error.
"""

import argparse
import json
import sys
import urllib.request
import urllib.error


SYSTEM_PROMPT = """\
You are a ReAct (Reason + Act) agent. Given a topic, you will produce \
a well-researched, structured document.

At each step, respond with EXACTLY ONE of:

  Thought: <your reasoning about what to research or write next>
  Action: search("<query>")    — to research a sub-topic
  Action: write("<paragraph>") — to draft a section
  Answer: <your final structured document>

Rules:
- Start with a Thought to plan your research.
- Use search to gather information, then write to compose sections.
- The Answer must be comprehensive, well-structured, and directly address the topic.
- Each step should be clearly labeled (Thought/Action/Answer).
- After your final Answer, do NOT add any more text.
"""


def call_ollama(ollama_url: str, model: str, prompt: str, context: str = "") -> str:
    """Call Ollama /api/chat and return the assistant's response text."""
    messages = [{"role": "system", "content": SYSTEM_PROMPT}]
    if context:
        messages.append({"role": "user", "content": f"Additional context: {context}"})
    messages.append({"role": "user", "content": prompt})

    payload = json.dumps({
        "model": model,
        "messages": messages,
        "stream": False,
        "options": {
            "temperature": 0.7,
            "num_predict": 2048,
            "keep_alive": "5m",
        },
    }).encode("utf-8")

    url = f"{ollama_url.rstrip('/')}/api/chat"
    req = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )

    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            return data.get("message", {}).get("content", "").strip()
    except urllib.error.URLError as exc:
        raise RuntimeError(f"Ollama request failed: {exc}") from exc
    except TimeoutError as exc:
        raise RuntimeError("Ollama request timed out (300s)") from exc


def _parse_step_response(response: str) -> tuple[str, str]:
    """Classify one ReAct loop step's response into a ``(kind, payload)`` pair.

    ``kind`` is the LLM-side marker that the parser detected. The caller
    is responsible for mapping ``search`` to the wire-format evidence
    type (``action``) so external JSON output stays byte-identical to the
    pre-fix contract.

    Order of checks matters because the markers can overlap (e.g.
    ``Action: search(`` is a subset char pattern that must NOT trip the
    ``Thought:`` branch first). The longest-prefix-first order below
    mirrors the pre-fix validator exactly:

      1. ``Answer:``  (terminal-state marker, must win first)
      2. ``Action: search(``  (info-gathering action)
      3. ``Action: write(``  (drafting action)
      4. ``Thought:``  (planning marker)
      5. fallback   (model did not follow format; treat the WHOLE response
         as the final answer — preserves the pre-fix "Model didn't
         follow format" branch).

    Args:
      response: raw assistant text for one ReAct step.

    Returns:
      (``kind``, ``payload``) where:

      * ``('thought', stripped_response)``
      * ``('search', stripped_response)``
      * ``('write', stripped_response)``
      * ``('answer', text_after_Answer_marker)``  (empty string if the
        model emitted a bare ``Answer:`` with no trailing text)
      * ``('answer', stripped_response)``  (fallback)
    """
    if "Answer:" in response:
        answer_part = response.split("Answer:", 1)[1].strip()
        return ("answer", answer_part)
    if "Action: search(" in response:
        return ("search", response.strip())
    if "Action: write(" in response:
        return ("write", response.strip())
    if "Thought:" in response:
        return ("thought", response.strip())
    # Model didn't follow format — preserve the pre-fix fallback that
    # treats the WHOLE response as the final answer.
    return ("answer", response.strip())


def run_react_loop(topic: str, context: str, max_steps: int,
                   ollama_url: str, model: str) -> dict:
    """Execute the ReAct reasoning loop and return the result dict."""
    evidence = []
    thinking_history = ""
    steps_taken = 0
    final_answer = ""

    for step in range(1, max_steps + 1):
        if step == 1:
            prompt = f"Research and write a comprehensive document about: {topic}"
        else:
            prompt = (
                f"Continue researching and writing about: {topic}.\n\n"
                f"What you have so far:\n{thinking_history[-2000:]}"
            )

        try:
            response = call_ollama(ollama_url, model, prompt, context)
        except RuntimeError as exc:
            return {
                "result": "",
                "status": "error",
                "steps_taken": steps_taken,
                "evidence": evidence,
                "error": str(exc),
            }

        steps_taken = step
        thinking_history += f"\n{response}\n"

        kind, payload = _parse_step_response(response)

        if kind == "answer":
            # If the parser returned a non-empty payload (real "Answer: "
            # marker with trailing text OR fallback WHOLE-RESPONSE), promote
            # it to final_answer. Empty payload: keep prior final_answer
            # — this preserves the pre-fix "Answer: <empty>" path where
            # the model emitted a bare marker with no payload.
            if payload:
                final_answer = payload
            evidence.append({"step": step, "type": "answer", "content": final_answer})
            break
        if kind == "search":
            # Parser returns 'search'; the outward evidence type stays
            # 'action' so external JSON consumers (Go-side parsers, audit
            # log readers) see NO wire-format drift.
            evidence.append({"step": step, "type": "action", "content": payload})
        elif kind == "write":
            evidence.append({"step": step, "type": "write", "content": payload})
        elif kind == "thought":
            evidence.append({"step": step, "type": "thought", "content": payload})

    if not final_answer:
        final_answer = thinking_history.strip()

    return {
        "result": final_answer,
        "status": "ok" if final_answer else "partial",
        "steps_taken": steps_taken,
        "evidence": evidence,
    }


def main():
    parser = argparse.ArgumentParser(description="ReAct agent bridge")
    parser.add_argument("--json", required=True, help="JSON payload with topic, context, max_steps, ollama_url, ollama_model")
    args = parser.parse_args()

    try:
        payload = json.loads(args.json)
    except json.JSONDecodeError as exc:
        print(json.dumps({"error": f"invalid JSON payload: {exc}", "status": "error"}))
        sys.exit(1)

    topic = payload.get("topic", "").strip()
    if not topic:
        print(json.dumps({"error": "topic is required", "status": "error"}))
        sys.exit(1)

    context = payload.get("context", "")
    max_steps = min(int(payload.get("max_steps", 5)), 10)
    ollama_url = payload.get("ollama_url", "http://localhost:11434")
    model = payload.get("ollama_model", "gemma4:e4b")

    result = run_react_loop(topic, context, max_steps, ollama_url, model)
    print(json.dumps(result))

    if result.get("status") == "error":
        sys.exit(1)


if __name__ == "__main__":
    main()
