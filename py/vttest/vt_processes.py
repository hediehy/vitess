# Copyright 2015 Google Inc. All Rights Reserved.

"""Starts the vtcombo process."""

import json
import logging
import os
import socket
import subprocess
import time
import urllib

from vttest import environment


class ShardInfo(object):
  """Contains the description for setting up a test shard.

  Every shard should have a unique db_name, since they're all stored in a single
  MySQL instance for the purpose of this test.
  """

  def __init__(self, keyspace, shard_name, db_name):
    self.keyspace = keyspace
    self.name = shard_name
    self.db_name = db_name


class VtProcess(object):
  """Base class for a vt process, vtcombo only now."""

  START_RETRIES = 5

  def __init__(self, name, directory, binary, port_name):
    self.name = name
    self.directory = directory
    self.binary = binary
    self.extraparams = []
    self.port_name = port_name
    self.process = None

  def wait_start(self):
    """Start the process and wait for it to respond on HTTP."""

    for _ in xrange(0, self.START_RETRIES):
      self.port = environment.get_port(self.port_name)
      if environment.get_protocol() == 'grpc':
        self.grpc_port = environment.get_port(self.port_name, protocol='grpc')
      else:
        self.grpc_port = None
      logs_subdirectory = environment.get_logs_directory(self.directory)
      cmd = [
          self.binary,
          '-port', '%u' % self.port,
          '-log_dir', logs_subdirectory,
          ]
      if environment.get_protocol() == 'grpc':
        cmd.extend(['-grpc_port', '%u' % self.grpc_port])
      cmd.extend(self.extraparams)
      logging.info('Starting process: %s', cmd)
      stdout = os.path.join(logs_subdirectory, '%s.%d.log' %
                            (self.name, self.port))
      self.stdout = open(stdout, 'w')
      self.process = subprocess.Popen(cmd,
                                      stdout=self.stdout,
                                      stderr=subprocess.STDOUT)
      timeout = time.time() + 60.0
      while time.time() < timeout:
        if environment.process_is_healthy(
            self.name, self.addr()) and self.get_vars():
          logging.info('%s started.', self.name)
          return
        elif self.process.poll() is not None:
          logging.error('%s process exited prematurely.', self.name)
          break
        time.sleep(0.3)

      logging.error('cannot start %s process on time: %s ',
                    self.name, socket.getfqdn())
      self.kill()

    raise Exception('Failed %d times to run %s' % (
        self.START_RETRIES,
        self.name))

  def addr(self):
    """Return the host:port of the process."""
    return '%s:%u' % (socket.getfqdn(), self.port)

  def grpc_addr(self):
    """Return the grpc host:port of the process.

    Only call this is environment.get_protocol() == 'grpc'."""
    return '%s:%u' % (socket.getfqdn(), self.grpc_port)

  def get_vars(self):
    """Return the debug vars."""
    data = None
    try:
      url = 'http://%s/debug/vars' % self.addr()
      f = urllib.urlopen(url)
      data = f.read()
      f.close()
    except IOError:
      return None
    try:
      return json.loads(data)
    except ValueError:
      logging.error('%s' % data)
      raise

  def kill(self):
    """Kill the process."""
    # These will proceed without error even if the process is already gone.
    self.process.terminate()

  def wait(self):
    """Wait for the process to end."""
    self.process.wait()


class VtcomboProcess(VtProcess):
  """Represents a vtcombo subprocess."""

  QUERYSERVER_PARAMETERS = [
      '-queryserver-config-pool-size', '4',
      '-queryserver-config-query-timeout', '300',
      '-queryserver-config-schema-reload-time', '60',
      '-queryserver-config-stream-pool-size', '4',
      '-queryserver-config-transaction-cap', '4',
      '-queryserver-config-transaction-timeout', '300',
      '-queryserver-config-txpool-timeout', '300',
      ]

  def __init__(self, directory, shards, mysql_db, vschema, charset):
    VtProcess.__init__(self, 'vtcombo-%s' % os.environ['USER'], directory,
                       environment.vtcombo_binary, port_name='vtcombo')
    topology = ",".join(["%s/%s:%s" % (shard.keyspace, shard.name,
                                       shard.db_name) for shard in shards])
    self.extraparams = [
        '-db-config-app-charset', charset,
        '-db-config-app-host', mysql_db.hostname(),
        '-db-config-app-port', str(mysql_db.port()),
        '-db-config-app-uname', mysql_db.username(),
        '-db-config-app-pass', mysql_db.password(),
        '-db-config-app-unixsocket', mysql_db.unix_socket(),
        '-topology', topology,
        '-mycnf_server_id', '1',
        '-mycnf_socket_file', mysql_db.unix_socket(),
    ] + self.QUERYSERVER_PARAMETERS + environment.extra_vtcombo_parameters()
    if vschema:
      self.extraparams.extend(['-vschema', vschema])


vtcombo_process = None


def start_vt_processes(directory, shards, mysql_db, vschema,
                       charset='utf8'):
  """Start the vt processes.

  Parameters:
    directory: the toplevel directory for the processes (logs, ...)
    shards: an array of ShardInfo objects.
    mysql_db: an instance of the mysql_db.MySqlDB class.
    charset: the character set for the database connections.
  """
  global vtcombo_process

  logging.info('start_vt_processes(directory=%s,vtcombo_binary=%s)',
               directory, environment.vtcombo_binary)
  vtcombo_process = VtcomboProcess(directory, shards, mysql_db, vschema, charset)
  vtcombo_process.wait_start()


def kill_vt_processes():
  """Call kill() on all processes."""
  logging.info('kill_vt_processes()')
  if vtcombo_process:
    vtcombo_process.kill()


def wait_vt_processes():
  """Call wait() on all processes."""
  logging.info('wait_vt_processes()')
  if vtcombo_process:
    vtcombo_process.wait()


def kill_and_wait_vt_processes():
  """Call kill() and then wait() on all processes."""
  kill_vt_processes()
  wait_vt_processes()


# wait_step is a helper for looping until a condition is true.
# use as follow:
#    timeout = 10
#    while True:
#      if done:
#        break
#      timeout = utils.wait_step('condition', timeout)
def wait_step(msg, timeout, sleep_time=1.0):
  timeout -= sleep_time
  if timeout <= 0:
    raise Exception("timeout waiting for condition '%s'" % msg)
  logging.debug("Sleeping for %f seconds waiting for condition '%s'",
                sleep_time, msg)
  time.sleep(sleep_time)
  return timeout
