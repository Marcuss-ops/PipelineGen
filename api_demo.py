import requests
import json
import time
import os
import subprocess

BASE_URL = "http://localhost:8080"

def print_section(title):
    print("\n" + "="*50)
    print(f" {title} ".center(50, "="))
    print("="*50)

def test_endpoint(method, path, data=None, headers=None):
    url = f"{BASE_URL}{path}"
    print(f"Testing {method} {path}...")
    try:
        if method == "GET":
            response = requests.get(url, headers=headers)
        elif method == "POST":
            response = requests.post(url, json=data, headers=headers)
        
        print(f"Status: {response.status_code}")
        try:
            print(f"Response: {json.dumps(response.json(), indent=2)}")
        except:
            print(f"Response (text): {response.text[:200]}")
        return response
    except Exception as e:
        print(f"Error: {e}")
        return None

def main():
    print_section("Health Check")
    test_endpoint("GET", "/health")

    print_section("Slug Generation")
    test_endpoint("GET", "/api/internal/slug?text=Hello World Pipeline Gen")

    print_section("Artlist Diagnostics")
    # This might require ARTLIST_ENABLED=true
    test_endpoint("GET", "/api/artlist/diagnostics")

    print_section("Jobs List")
    test_endpoint("GET", "/api/jobs?limit=5")

    print_section("System Info (Fake example if exists)")
    test_endpoint("GET", "/api/health")

if __name__ == "__main__":
    main()
