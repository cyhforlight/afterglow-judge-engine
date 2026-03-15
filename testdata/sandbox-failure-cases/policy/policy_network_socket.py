import socket
import sys

try:
    # Attempt to create a socket (should be blocked by seccomp)
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    print("Socket creation succeeded (seccomp NOT working!)")
    sys.exit(1)  # Failure - seccomp should have blocked this
except OSError as e:
    print(f"Socket creation blocked: {e} (expected with seccomp)")
    sys.exit(0)  # Success - seccomp is working
