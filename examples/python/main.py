"""
kiri Python SDK Integration Example
====================================
Make sure to set the environment variables before running:
    export STORAGE_EMULATOR_HOST="http://localhost:4443"
    export PUBSUB_EMULATOR_HOST="localhost:8085"
"""

import os
import requests

# Ensure emulator env vars are set
os.environ.setdefault("STORAGE_EMULATOR_HOST", "http://localhost:4443")
os.environ.setdefault("PUBSUB_EMULATOR_HOST", "localhost:8085")

from google.cloud import storage

def main():
    print("=== kiri Python SDK Integration Example ===")
    
    # 1. Connect Google Cloud Storage client to local kiri
    client = storage.Client()
    bucket_name = "py-app-bucket"
    
    # Create bucket
    bucket = client.create_bucket(bucket_name)
    print(f"✓ Created GCS bucket: {bucket.name}")
    
    # Upload blob
    blob = bucket.blob("sample.json")
    blob.upload_from_string('{"status": "ok", "emulator": "kiri"}', content_type="application/json")
    print("✓ Uploaded sample.json to bucket")
    
    # Download blob
    downloaded_data = blob.download_as_text()
    print(f"✓ Downloaded blob content: {downloaded_data}")
    
    # 2. Query kiri Cost Calculator
    response = requests.post(
        "http://localhost:4443/kiri/billing/calculator",
        json={
            "resources": [
                {"service": "cloudrun", "requestsPerMonth": 1000000, "cpu": 1.0, "memoryGb": 1.0},
                {"service": "firestore", "readsPerDay": 50000, "writesPerDay": 20000, "storageGb": 10.0}
            ]
        }
    )
    print("✓ Calculated monthly architecture cost:")
    print(response.json())

if __name__ == "__main__":
    main()
