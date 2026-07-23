/**
 * kiri Node.js / TypeScript SDK Integration Example
 * ==================================================
 * Set environment variable:
 *   export PUBSUB_EMULATOR_HOST="localhost:8085"
 */

const { Storage } = require('@google-cloud/storage');
const http = require('http');

async function main() {
  console.log('=== kiri Node.js SDK Integration Example ===');

  // 1. Initialize Storage client connected to local kiri
  const storage = new Storage({
    apiEndpoint: 'http://localhost:4443',
    projectId: 'local-node-project',
  });

  const bucketName = 'node-app-bucket';

  try {
    // Create bucket
    await storage.createBucket(bucketName);
    console.log(`✓ Created GCS bucket: ${bucketName}`);

    // Upload file
    const file = storage.bucket(bucketName).file('data.txt');
    await file.save('Hello from Node.js GCP SDK running on kiri!');
    console.log('✓ Uploaded data.txt to bucket');

    // Read file back
    const [content] = await file.download();
    console.log(`✓ Read file content: ${content.toString()}`);
  } catch (err) {
    console.error('Storage operation error:', err.message);
  }

  // 2. Query kiri Cost Estimation API
  const calcData = JSON.stringify({
    resources: [
      { service: 'cloudrun', requestsPerMonth: 3000000, cpu: 2.0, memoryGb: 4.0 },
      { service: 'pubsub', messagesPerMonth: 10000000, messageSizeBytes: 1024 }
    ]
  });

  const req = http.request(
    'http://localhost:4443/kiri/billing/calculator',
    { method: 'POST', headers: { 'Content-Type': 'application/json' } },
    (res) => {
      let body = '';
      res.on('data', (chunk) => (body += chunk));
      res.on('end', () => {
        console.log('✓ Calculated architecture cost estimate:');
        console.log(JSON.parse(body));
      });
    }
  );
  req.write(calcData);
  req.end();
}

main();
