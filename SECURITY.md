# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems. Email **partners@noderings.com** with
a description, steps to reproduce and the SDK version. We acknowledge reports within three
working days and credit you in the release notes unless you ask us not to.

## Supported versions

Security fixes are made for the latest minor release.

## Handling credentials

The SDK sends your token only to the base URL you configure (`https://api.noderings.com` by
default), in the `Authorization` header. It never logs tokens or request and response bodies.
Use a service account with only the permissions your program needs.
