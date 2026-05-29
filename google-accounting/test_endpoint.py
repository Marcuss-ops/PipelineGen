import urllib.request
import json
import time

url = "http://127.0.0.1:8000/generate-flow-images"
data = {
    "prompt": "un cane allegro nel parco al tramonto, 16:9",
    "num_images": 3,
    "project_id": "1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4",
    "style": "realistic",
    "headless": True,
    "account": "favamassimo",
    "drive_folder_id": "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk"
}

req = urllib.request.Request(
    url, 
    data=json.dumps(data).encode("utf-8"),
    headers={"Content-Type": "application/json"}
)

print("Triggering /generate-flow-images...")
try:
    with urllib.request.urlopen(req) as resp:
        res = json.loads(resp.read().decode())
        print("Response:", res)
        job_id = res["job_id"]
except Exception as e:
    print("Error:", e)
    exit(1)

# Poll for status
status_url = f"http://127.0.0.1:8000/status/{job_id}"
print(f"Polling job {job_id} at {status_url}...")
for _ in range(60):
    try:
        with urllib.request.urlopen(status_url) as resp:
            job = json.loads(resp.read().decode())
            print(f"Status: {job.get('status')} | Step: {job.get('current_step')} | Log: {job.get('last_log')}")
            if job.get("status") in ("done", "completed"):
                print("SUCCESS!")
                print("Files generated:", job.get("files"))
                print("Drive uploads:", job.get("drive_uploads"))
                break
            elif job.get("status") == "failed":
                print("FAILED:", job.get("error"))
                break
    except Exception as e:
        print("Polling error:", e)
    time.sleep(5)
