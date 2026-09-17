#!/usr/bin/env python3
"""Disposable VM canary; start only after root approves the worker trial.

Both ports echo a public sentinel. No filesystem or machine information is served.
Every TCP/UDP request appends to the supplied log, so lack of a client response
alone is never mistaken for successful blocking.
"""
import argparse
import http.server
import json
import socketserver
import threading
import time

parser = argparse.ArgumentParser()
parser.add_argument("--tcp-port", type=int, default=18081)
parser.add_argument("--udp-port", type=int, default=18082)
parser.add_argument("--log", required=True)
args = parser.parse_args()
lock = threading.Lock()


def record(protocol, peer):
    with lock, open(args.log, "a") as output:
        output.write(json.dumps({"at": time.time(), "protocol": protocol, "peer": peer[0], "port": peer[1]}) + "\n")


class HTTP(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        record("tcp", self.client_address)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"cube-isolation-sentinel")

    def log_message(self, *unused):
        pass


class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        record("udp", self.client_address)
        _, sock = self.request
        sock.sendto(b"cube-isolation-sentinel", self.client_address)


udp = socketserver.ThreadingUDPServer(("0.0.0.0", args.udp_port), UDP)
threading.Thread(target=udp.serve_forever, daemon=True).start()
http.server.ThreadingHTTPServer(("0.0.0.0", args.tcp_port), HTTP).serve_forever()
