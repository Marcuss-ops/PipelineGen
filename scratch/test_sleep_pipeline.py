import urllib.request
import json

url = "http://127.0.0.1:8081/api/script/generate-from-source"
source_text = """Sleep without pills starts long before you turn out the light. It is built in daylight, in the small choices about caffeine and screens, in the way you teach your brain that bed means sleep. The work is less dramatic and more reliable: anchor your body clock, cool the room, empty the mind, and repeat."""

data = {
    "source_text": source_text.strip(),
    "language": "en",
    "style": "whiteboard",
    "visual_style": "whiteboard",
    "scene_count": 1,
    "width": 1024,
    "height": 1024,
    "title": "Sleep Without Pills",
    "output_name": "sleep-without-pills"
}

req = urllib.request.Request(
    url, 
    data=json.dumps(data).encode("utf-8"),
    headers={"Content-Type": "application/json"}
)

print("Triggering Go pipeline endpoint with Sleep Routine in Whiteboard Style...")
try:
    with urllib.request.urlopen(req, timeout=1200) as resp:
        res = json.loads(resp.read().decode())
        print("Pipeline Execution Success!")
        print("Document URL:", res.get("doc_url"))
        print("Markdown Path:", res.get("markdown_path"))
        print("JSON Path:", res.get("json_path"))
        print("Generated scenes count:", len(res.get("scenes", [])))
except Exception as e:
    print("Error:", e)
    if hasattr(e, "read"):
        print("Detail:", e.read().decode())
