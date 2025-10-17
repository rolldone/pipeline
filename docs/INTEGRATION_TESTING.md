Integration testing guide for rsync_file steps

This document describes how to run integration tests that exercise the rsync-based `rsync_file` step.

Overview
--------
Integration tests require an SSH server with `rsync` accessible. The recommended approach is to run a lightweight Docker container that provides OpenSSH server and rsync binary and configure keys for passwordless access from the test runner.

Suggested Docker image
----------------------
Use a minimal image like `rastasheep/ubuntu-sshd` and install `rsync` in it, or use a custom Dockerfile:

```Dockerfile
FROM ubuntu:22.04
RUN apt-get update && apt-get install -y openssh-server rsync && mkdir /var/run/sshd
# create test user
RUN useradd -m -s /bin/bash testuser && echo "testuser:test" | chpasswd
# allow password auth for simplicity in test container
RUN sed -i 's/PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config || true
EXPOSE 22
CMD ["/usr/sbin/sshd","-D"]
```

Test flow
---------
1. Start container with port mapping, mount a host directory as remote data store.
2. Generate an SSH keypair on the CI runner or use an existing test key.
3. Copy the public key into `/home/testuser/.ssh/authorized_keys` inside the container.
4. Run pipeline with a test execution configured to point to the container host (e.g., `localhost:2222`) and use the SSH alias in `pipeline.yaml`.
5. Use `rsync_file` steps with `delete_policy: soft` and `force` and assert changes on the remote mount.

Automation tips
---------------
- Use `docker run --rm -d -p 2222:22 -v $(pwd)/test-remote:/remote test-rsync-image` to spin up container.
- Use `ssh-keygen -t rsa -b 2048 -f /tmp/testkey -N ''` then `docker cp /tmp/testkey.pub <container>:/home/testuser/.ssh/authorized_keys`.
- Teardown container after tests.

CI configuration
----------------
- Add a separate job for integration tests that starts the container service.
- Use `go test -tags=integration ./...` for integration tests and mark them as slow.

Notes
-----
- Windows runners will need Docker support; otherwise run integration tests on Linux CI only.
- Consider using `sshd` base images that already include `rsync` to simplify setup.
