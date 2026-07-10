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

        # Parse the response type
        if "Answer:" in response:
            answer_part = response.split("Answer:", 1)[1].strip()
            if answer_part:
                final_answer = answer_part
            evidence.append({"step": step, "type": "answer", "content": final_answer})
            break
        elif "Action: search(" in response:
            evidence.append({"step": step, "type": "action", "content": response.strip()})
        elif "Action: write(" in response:
            evidence.append({"step": step, "type": "write", "content": response.strip()})
        elif "Thought:" in response:
            evidence.append({"step": step, "type": "thought", "content": response.strip()})
        else:
            # Model didn't follow format — treat as answer
            final_answer = response
            evidence.append({"step": step, "type": "answer", "content": response.strip()})
            break

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
