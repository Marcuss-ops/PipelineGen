import urllib.request
import json

url = "http://127.0.0.1:8081/api/script/generate-from-source"
data = {
    "source_text": "In a quiet forest, a tiny squirrel found a golden acorn. The squirrel hid the acorn under a large oak tree. A curious owl watched the squirrel from above. The next day, the acorn started to glow. The squirrel and the owl realized it was a magical acorn.",
    "language": "en",
    "style": "documentary",
    "visual_style": "realistic",
    "scene_count": 1,
    "width": 1024,
    "height": 1024
}

req = urllib.request.Request(
    url, 
    data=json.dumps(data).encode("utf-8"),
    headers={"Content-Type": "application/json"}
)

print("Triggering Go pipeline endpoint...")
try:
    with urllib.request.urlopen(req, timeout=300) as resp:
        res = json.loads(resp.read().decode())
        print("Pipeline Execution Success!")
        print(json.dumps(res, indent=2))
except Exception as e:
    print("Error:", e)
    if hasattr(e, "read"):
        print("Detail:", e.read().decode())
