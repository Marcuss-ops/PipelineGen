import urllib.request
import json

url = "http://127.0.0.1:8081/api/script/generate-from-source"
data = {
    "source_text": "In the rain-slicked streets of Neo-Tokyo, towering holographic advertisements illuminated the dark alleys. Cybernetically enhanced citizens walked under neon billboards, carrying glowing datapads. A rogue netrunner in a dark leather jacket slipped past security drones, plugging a glowing shard into the main mainframe terminal.",
    "language": "en",
    "style": "cyberpunk",
    "visual_style": "cyberpunk",
    "scene_count": 2,
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
