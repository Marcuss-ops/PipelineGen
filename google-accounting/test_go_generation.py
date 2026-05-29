import urllib.request
import json
import time

url = "http://127.0.0.1:8081/api/images/generate"
data = {
    "prompt": "un favoloso tramonto infuocato sulle montagne alpine con lago riflesso",
    "style": "landscape",
    "tags": ["test-parallel", "landscape"]
}

req = urllib.request.Request(
    url, 
    data=json.dumps(data).encode("utf-8"),
    headers={"Content-Type": "application/json"}
)

print("Triggering Go Service Image Generation...")
try:
    with urllib.request.urlopen(req) as resp:
        res = json.loads(resp.read().decode())
        print("Success response:")
        print(json.dumps(res, indent=2))
except Exception as e:
    print("Error:", e)
    # Check if there is detailed response body in case of error
    if hasattr(e, "read"):
        print("Detail:", e.read().decode())
