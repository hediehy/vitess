#!/usr/bin/env python

# Copyright 2015 Google Inc. All rights reserved.
# Use of this source code is governed by a BSD-style license that can
# be found in the LICENSE file.

"""This is a demo for V3 features. The script will launch all the
processes necessary to bring up the demo. It will bring up an HTTP
server on port 8000 by default, which you can override. Once done,
hitting <Enter> will terminate all processes.
Vitess will always be started on port 12345.
"""

import json
import optparse
import os
import subprocess
import thread

from CGIHTTPServer import CGIHTTPRequestHandler
from BaseHTTPServer import HTTPServer


def start_http_server(port):
  httpd = HTTPServer(('', port), CGIHTTPRequestHandler)
  thread.start_new_thread(httpd.serve_forever, ())

def start_vitess():
  vttop = os.environ['VTTOP']
  args = [os.path.join(vttop, 'py/vttest/run_local_database.py'),
          '--port', '12345',
          '--topology', 'user/-80:user0,user/80-:user1,lookup/0:lookup',
          '--schema_dir', os.path.join(vttop, 'examples/demo/schema'),
          '--vschema', os.path.join(vttop, 'examples/demo/schema/vschema.json')]
  sp = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE)

  # This load will make us wait for vitess to come up.
  json.loads(sp.stdout.readline())
  return sp

def stop_vitess(sp):
  sp.stdin.write('\n')
  sp.wait()


def main():
  parser = optparse.OptionParser()
  parser.add_option('-p', '--port', default=8000, help='http server port')
  (options, args) = parser.parse_args()

  sp = start_vitess()
  try:
    start_http_server(options.port)
    raw_input("press enter to exit.")
  finally:
    stop_vitess(sp)


if __name__ == '__main__':
  main()
