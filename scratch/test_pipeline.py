import urllib.request
import json

url = "http://127.0.0.1:8081/api/script/generate-from-source"
data = {
    "source_text": "A majestic spaceship entered the orbit of a mysterious alien planet. The ship lowered its thrusters and slowly descended through the thick violet atmosphere. Below lay a glowing crystal city, reflecting the light of two moons. As the doors opened, an astronaut stepped out onto the metallic landing pad, looking at the towering alien spires.",
    "language": "en",
    "style": "documentary",
    "visual_style": "realistic",
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
