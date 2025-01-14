# Copyright 2015 Google Inc. All Rights Reserved.

"""Contains environment specifications for vttest module.

This module is meant to be overwritten upon import into a development
tree with the appropriate values. It works as is in the Vitess tree.
"""

import os
import shutil
import tempfile

# this is the location of the vtcombo binary
vtcombo_binary = os.path.join(os.environ['VTROOT'], 'bin', 'vtcombo')

# this is the location of the mysqlctl binary, if mysql_db_mysqlctl is used.
mysqlctl_binary = os.path.join(os.environ['VTROOT'], 'bin', 'mysqlctl')

# this is the base port set by options.
base_port = None

# this is the class to use for MySqlDB instances
mysql_db_class = None


def get_test_directory():
  """Returns the toplevel directory for the tests. Might create it."""
  directory = tempfile.mkdtemp(prefix='vttest', dir=os.environ.get('VTDATAROOT', None))
  # Override VTDATAROOT to point to the newly created dir
  os.environ['VTDATAROOT'] = directory
  os.mkdir(get_logs_directory(directory))
  return directory


def get_logs_directory(directory):
  """Returns the directory for logs, might be based on directory.

  Parameters:
    directory: the value returned by get_test_directory().
  """
  return os.path.join(directory, 'logs')


def cleanup_test_directory(directory):
  """Cleans up the test directory after the test is done.

  Parameters:
    directory: the value returned by get_test_directory().
  """
  shutil.rmtree(directory)


def extra_vtcombo_parameters():
  """Returns extra parameters to send to vtcombo."""
  return [
    '-service_map', 'grpc-vtgateservice,bsonrpc-vt-vtgateservice',
  ]


def process_is_healthy(name, addr):
  """Double-checks a process is healthy and ready for RPCs."""
  return True


def get_protocol():
  """Returns the protocol used between client and vtcombo."""
  return 'grpc'


def get_port(name, protocol=None):
  """Returns the port to use for a given process.

  This is only called once per process, so picking an unused port will also work.
  """
  if name == 'vtcombo':
    port = base_port
  elif name == 'mysql':
    port = base_port + 2
  else:
    raise ValueError('name should be vtcombo or mysql, not %s' % name)

  if protocol == 'grpc':
    port += 1

  return port
