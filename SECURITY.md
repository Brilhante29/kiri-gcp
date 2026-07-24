# Security Policy

## Scope, in plain terms

kiri is a **local development emulator**. It runs with no authentication by
design and is meant for your machine or CI, never as a public, internet-facing
service. Do not store real secrets or production data in it, and do not expose
its ports to an untrusted network.

## Supported versions

The `main` branch and the latest release receive security fixes.

## Reporting a vulnerability

Please do not open a public issue for security problems.

Use GitHub's private vulnerability reporting: go to the **Security** tab of the
repository and choose **Report a vulnerability**. If that is unavailable, contact
[@Brilhante29](https://github.com/Brilhante29) directly.

Include what you can: affected service or endpoint, a reproduction, and the
impact. You will get an acknowledgement, and we will coordinate a fix and
disclosure timeline with you.
