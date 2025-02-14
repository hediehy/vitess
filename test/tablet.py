import json
import logging
import os
import shutil
import sys
import time
import urllib2
import warnings
import re
# Dropping a table inexplicably produces a warning despite
# the 'IF EXISTS' clause. Squelch these warnings.
warnings.simplefilter('ignore')

import MySQLdb

import environment
import utils
from mysql_flavor import mysql_flavor
from protocols_flavor import protocols_flavor


tablet_cell_map = {
    62344: 'nj',
    62044: 'nj',
    41983: 'nj',
    31981: 'ny',
}


def get_backup_storage_flags():
  return ['-backup_storage_implementation', 'file',
          '-file_backup_storage_root',
          os.path.join(environment.tmproot, 'backupstorage')]


def get_all_extra_my_cnf(extra_my_cnf):
  all_extra_my_cnf = [environment.vttop + '/config/mycnf/default-fast.cnf']
  flavor_my_cnf = mysql_flavor().extra_my_cnf()
  if flavor_my_cnf:
    all_extra_my_cnf.append(flavor_my_cnf)
  if extra_my_cnf:
    all_extra_my_cnf.append(extra_my_cnf)
  return all_extra_my_cnf


class Tablet(object):
  """This class helps manage a vttablet instance.

  To use it for vttablet, you need to use init_tablet and/or
  start_vttablet.
  """
  default_uid = 62344
  seq = 0
  tablets_running = 0
  default_db_config = {
      'app': {
          'uname': 'vt_app',
          'charset': 'utf8'
      },
      'dba': {
          'uname': 'vt_dba',
          'charset': 'utf8'
      },
      'filtered': {
          'uname': 'vt_filtered',
          'charset': 'utf8'
      },
      'repl': {
          'uname': 'vt_repl',
          'charset': 'utf8'
      }
  }

  # this will eventually be coming from the proto3
  tablet_type_value = {
      'UNKNOWN': 0,
      'MASTER': 1,
      'REPLICA': 2,
      'RDONLY': 3,
      'BATCH': 3,
      'SPARE': 4,
      'EXPERIMENTAL': 5,
      'BACKUP': 6,
      'RESTORE': 7,
      'WORKER': 8,
  }

  def __init__(self, tablet_uid=None, port=None, mysql_port=None, cell=None,
               use_mysqlctld=False):
    self.tablet_uid = tablet_uid or (Tablet.default_uid + Tablet.seq)
    self.port = port or (environment.reserve_ports(1))
    self.mysql_port = mysql_port or (environment.reserve_ports(1))
    self.grpc_port = environment.reserve_ports(1)
    self.use_mysqlctld = use_mysqlctld
    Tablet.seq += 1

    if cell:
      self.cell = cell
    else:
      self.cell = tablet_cell_map.get(tablet_uid, 'nj')
    self.proc = None

    # filled in during init_tablet
    self.keyspace = None
    self.shard = None

    # utility variables
    self.tablet_alias = 'test_%s-%010d' % (self.cell, self.tablet_uid)
    self.zk_tablet_path = (
        '/zk/test_%s/vt/tablets/%010d' % (self.cell, self.tablet_uid))

  def __str__(self):
    return 'tablet: uid: %d web: http://localhost:%d/ rpc port: %d' % (
        self.tablet_uid, self.port, self.grpc_port)

  def update_stream_python_endpoint(self):
    protocol = protocols_flavor().binlog_player_python_protocol()
    port = self.port
    if protocol == 'gorpc':
      from vtdb import gorpc_update_stream
    elif protocol == 'grpc':
      # import the grpc update stream client implementation, change the port
      from vtdb import grpc_update_stream
      port = self.grpc_port
    return (protocol, 'localhost:%d' % port)

  def mysqlctl(self, cmd, extra_my_cnf=None, with_ports=False, verbose=False):
    extra_env = {}
    all_extra_my_cnf = get_all_extra_my_cnf(extra_my_cnf)
    if all_extra_my_cnf:
      extra_env['EXTRA_MY_CNF'] = ':'.join(all_extra_my_cnf)
    args = environment.binary_args('mysqlctl') + [
        '-log_dir', environment.vtlogroot,
        '-tablet_uid', str(self.tablet_uid)]
    if self.use_mysqlctld:
      args.extend(
          ['-mysqlctl_socket', os.path.join(self.tablet_dir, 'mysqlctl.sock')])
    if with_ports:
      args.extend(['-port', str(self.port),
                   '-mysql_port', str(self.mysql_port)])
    self._add_dbconfigs(args)
    if verbose:
      args.append('-alsologtostderr')
    args.extend(cmd)
    return utils.run_bg(args, extra_env=extra_env)

  def mysqlctld(self, cmd, extra_my_cnf=None, verbose=False):
    extra_env = {}
    all_extra_my_cnf = get_all_extra_my_cnf(extra_my_cnf)
    if all_extra_my_cnf:
      extra_env['EXTRA_MY_CNF'] = ':'.join(all_extra_my_cnf)
    args = environment.binary_args('mysqlctld') + [
        '-log_dir', environment.vtlogroot,
        '-tablet_uid', str(self.tablet_uid),
        '-mysql_port', str(self.mysql_port),
        '-socket_file', os.path.join(self.tablet_dir, 'mysqlctl.sock')]
    self._add_dbconfigs(args)
    if verbose:
      args.append('-alsologtostderr')
    args.extend(cmd)
    return utils.run_bg(args, extra_env=extra_env)

  def init_mysql(self, extra_my_cnf=None):
    if self.use_mysqlctld:
      return self.mysqlctld(
          ['-init_db_sql_file', environment.vttop + '/config/init_db.sql'],
          extra_my_cnf=extra_my_cnf)
    else:
      return self.mysqlctl(
          ['init', '-init_db_sql_file',
           environment.vttop + '/config/init_db.sql'],
          extra_my_cnf=extra_my_cnf, with_ports=True)

  def start_mysql(self):
    return self.mysqlctl(['start'], with_ports=True)

  def shutdown_mysql(self):
    return self.mysqlctl(['shutdown'], with_ports=True)

  def teardown_mysql(self):
    if utils.options.keep_logs:
      return self.shutdown_mysql()
    return self.mysqlctl(['teardown', '-force'])

  def remove_tree(self):
    if utils.options.keep_logs:
      return
    try:
      shutil.rmtree(self.tablet_dir)
    except OSError as e:
      if utils.options.verbose == 2:
        print >> sys.stderr, e, self.tablet_dir

  def mysql_connection_parameters(self, dbname, user='vt_dba'):
    return dict(user=user,
                unix_socket=self.tablet_dir + '/mysql.sock',
                db=dbname)

  def connect(self, dbname='', user='vt_dba', **params):
    params.update(self.mysql_connection_parameters(dbname, user))
    conn = MySQLdb.Connect(**params)
    return conn, conn.cursor()

  def connect_dict(self, dbname='', user='vt_dba', **params):
    params.update(self.mysql_connection_parameters(dbname, user))
    conn = MySQLdb.Connect(**params)
    return conn, MySQLdb.cursors.DictCursor(conn)

  # Query the MySQL instance directly
  def mquery(
      self, dbname, query, write=False, user='vt_dba', conn_params=None):
    if conn_params is None:
      conn_params = {}
    conn, cursor = self.connect(dbname, user=user, **conn_params)
    if write:
      conn.begin()
    if isinstance(query, basestring):
      query = [query]

    for q in query:
      # logging.debug('mysql(%s,%s): %s', self.tablet_uid, dbname, q)
      cursor.execute(q)

    if write:
      conn.commit()

    try:
      return cursor.fetchall()
    finally:
      conn.close()

  def assert_table_count(self, dbname, table, n, where=''):
    result = self.mquery(dbname, 'select count(*) from ' + table + ' ' + where)
    if result[0][0] != n:
      raise utils.TestError('expected %d rows in %s' % (n, table), result)

  def reset_replication(self):
    self.mquery('', mysql_flavor().reset_replication_commands())

  def populate(self, dbname, create_sql, insert_sqls=[]):
    self.create_db(dbname)
    if isinstance(create_sql, basestring):
      create_sql = [create_sql]
    for q in create_sql:
      self.mquery(dbname, q)
    for q in insert_sqls:
      self.mquery(dbname, q, write=True)

  def has_db(self, name):
    rows = self.mquery('', 'show databases')
    for row in rows:
      dbname = row[0]
      if dbname == name:
        return True
    return False

  def drop_db(self, name):
    self.mquery('', 'drop database if exists %s' % name)
    while self.has_db(name):
      logging.debug('%s sleeping while waiting for database drop: %s',
                    self.tablet_alias, name)
      time.sleep(0.3)
      self.mquery('', 'drop database if exists %s' % name)

  def create_db(self, name):
    self.drop_db(name)
    self.mquery('', 'create database %s' % name)

  def clean_dbs(self):
    logging.debug('mysql(%s): removing all databases', self.tablet_uid)
    rows = self.mquery('', 'show databases')
    for row in rows:
      dbname = row[0]
      if dbname in ['information_schema', 'mysql']:
        continue
      self.drop_db(dbname)

  def wait_check_db_var(self, name, value):
    for _ in range(3):
      try:
        return self.check_db_var(name, value)
      except utils.TestError as e:
        print >> sys.stderr, 'WARNING: ', e
      time.sleep(1.0)
    raise e

  def check_db_var(self, name, value):
    row = self.get_db_var(name)
    if row != (name, value):
      raise utils.TestError('variable not set correctly', name, row)

  def get_db_var(self, name):
    conn, cursor = self.connect()
    try:
      cursor.execute("show variables like '%s'" % name)
      return cursor.fetchone()
    finally:
      conn.close()

  def update_addrs(self):
    args = [
        'UpdateTabletAddrs',
        '-hostname', 'localhost',
        '-ip-addr', '127.0.0.1',
        '-mysql-port', '%d' % self.mysql_port,
        '-vt-port', '%d' % self.port,
        self.tablet_alias
    ]
    return utils.run_vtctl(args)

  def init_tablet(self, tablet_type, keyspace, shard,
                  start=False, dbname=None, parent=True, wait_for_start=True,
                  include_mysql_port=True, **kwargs):
    self.tablet_type = tablet_type
    self.keyspace = keyspace
    self.shard = shard

    self.dbname = dbname or ('vt_' + self.keyspace)

    args = ['InitTablet',
            '-hostname', 'localhost',
            '-port', str(self.port)]
    if include_mysql_port:
      args.extend(['-mysql_port', str(self.mysql_port)])
    if parent:
      args.append('-parent')
    if dbname:
      args.extend(['-db_name_override', dbname])
    if keyspace:
      args.extend(['-keyspace', keyspace])
    if shard:
      args.extend(['-shard', shard])
    args.extend([self.tablet_alias, tablet_type])
    utils.run_vtctl(args)
    if start:
      if not wait_for_start:
        expected_state = None
      elif (tablet_type == 'master' or tablet_type == 'replica' or
            tablet_type == 'rdonly' or tablet_type == 'batch'):
        expected_state = 'SERVING'
      else:
        expected_state = 'NOT_SERVING'
      self.start_vttablet(wait_for_state=expected_state, **kwargs)

  @property
  def tablet_dir(self):
    return '%s/vt_%010d' % (environment.vtdataroot, self.tablet_uid)

  def grpc_enabled(self):
    return (
        protocols_flavor().tabletconn_protocol() == 'grpc' or
        protocols_flavor().tablet_manager_protocol() == 'grpc' or
        protocols_flavor().binlog_player_protocol() == 'grpc')

  def flush(self):
    utils.curl('http://localhost:%s%s' %
               (self.port, environment.flush_logs_url),
               stderr=utils.devnull, stdout=utils.devnull)

  def start_vttablet(
      self, port=None, memcache=False,
      wait_for_state='SERVING', filecustomrules=None, zkcustomrules=None,
      schema_override=None,
      repl_extra_flags=None, table_acl_config=None,
      lameduck_period=None, security_policy=None,
      target_tablet_type=None, full_mycnf_args=False,
      extra_args=None, extra_env=None, include_mysql_port=True,
      init_tablet_type=None, init_keyspace=None,
      init_shard=None, init_db_name_override=None,
      supports_backups=False):
    """Starts a vttablet process, and returns it.

    The process is also saved in self.proc, so it's easy to kill as well.
    """
    args = environment.binary_args('vttablet')
    # Use 'localhost' as hostname because Travis CI worker hostnames
    # are too long for MySQL replication.
    args.extend(['-tablet_hostname', 'localhost'])
    args.extend(['-tablet-path', self.tablet_alias])
    args.extend(environment.topo_server().flags())
    args.extend(['-binlog_player_protocol',
                 protocols_flavor().binlog_player_protocol()])
    args.extend(['-tablet_manager_protocol',
                 protocols_flavor().tablet_manager_protocol()])
    args.extend(['-tablet_protocol', protocols_flavor().tabletconn_protocol()])
    args.extend(['-binlog_player_healthcheck_topology_refresh', '1s'])
    args.extend(['-binlog_player_retry_delay', '1s'])
    args.extend(['-pid_file', os.path.join(self.tablet_dir, 'vttablet.pid')])
    if self.use_mysqlctld:
      args.extend(
          ['-mysqlctl_socket', os.path.join(self.tablet_dir, 'mysqlctl.sock')])

    if full_mycnf_args:
      # this flag is used to specify all the mycnf_ flags, to make
      # sure that code works.
      relay_log_path = os.path.join(self.tablet_dir, 'relay-logs',
                                    'vt-%010d-relay-bin' % self.tablet_uid)
      args.extend([
          '-mycnf_server_id', str(self.tablet_uid),
          '-mycnf_data_dir', os.path.join(self.tablet_dir, 'data'),
          '-mycnf_innodb_data_home_dir', os.path.join(self.tablet_dir,
                                                      'innodb', 'data'),
          '-mycnf_innodb_log_group_home_dir', os.path.join(self.tablet_dir,
                                                           'innodb', 'logs'),
          '-mycnf_socket_file', os.path.join(self.tablet_dir, 'mysql.sock'),
          '-mycnf_error_log_path', os.path.join(self.tablet_dir, 'error.log'),
          '-mycnf_slow_log_path', os.path.join(self.tablet_dir,
                                               'slow-query.log'),
          '-mycnf_relay_log_path', relay_log_path,
          '-mycnf_relay_log_index_path', relay_log_path + '.index',
          '-mycnf_relay_log_info_path', os.path.join(self.tablet_dir,
                                                     'relay-logs',
                                                     'relay-log.info'),
          '-mycnf_bin_log_path', os.path.join(
              self.tablet_dir, 'bin-logs', 'vt-%010d-bin' % self.tablet_uid),
          '-mycnf_master_info_file', os.path.join(self.tablet_dir,
                                                  'master.info'),
          '-mycnf_pid_file', os.path.join(self.tablet_dir, 'mysql.pid'),
          '-mycnf_tmp_dir', os.path.join(self.tablet_dir, 'tmp'),
          '-mycnf_slave_load_tmp_dir', os.path.join(self.tablet_dir, 'tmp'),
      ])
      if include_mysql_port:
        args.extend(['-mycnf_mysql_port', str(self.mysql_port)])
    if target_tablet_type:
      self.tablet_type = target_tablet_type
      args.extend(['-target_tablet_type', target_tablet_type,
                   '-health_check_interval', '2s',
                   '-enable_replication_lag_check',
                   '-degraded_threshold', '5s'])

    # this is used to run InitTablet as part of the vttablet startup
    if init_tablet_type:
      self.tablet_type = init_tablet_type
      args.extend(['-init_tablet_type', init_tablet_type])
    if init_keyspace:
      self.keyspace = init_keyspace
      self.shard = init_shard
      args.extend(['-init_keyspace', init_keyspace,
                   '-init_shard', init_shard])
      if init_db_name_override:
        self.dbname = init_db_name_override
        args.extend(['-init_db_name_override', init_db_name_override])
      else:
        self.dbname = 'vt_' + init_keyspace

    if supports_backups:
      args.extend(['-restore_from_backup'] + get_backup_storage_flags())

    if extra_args:
      args.extend(extra_args)

    args.extend(['-port', '%s' % (port or self.port),
                 '-log_dir', environment.vtlogroot])

    self._add_dbconfigs(args, repl_extra_flags)

    if memcache:
      args.extend(['-rowcache-bin', environment.memcached_bin()])
      memcache_socket = os.path.join(self.tablet_dir, 'memcache.sock')
      args.extend(['-rowcache-socket', memcache_socket])
      args.extend(['-enable-rowcache'])

    if filecustomrules:
      args.extend(['-filecustomrules', filecustomrules])
    if zkcustomrules:
      args.extend(['-zkcustomrules', zkcustomrules])

    if schema_override:
      args.extend(['-schema-override', schema_override])

    if table_acl_config:
      args.extend(['-table-acl-config', table_acl_config])
      args.extend(['-queryserver-config-strict-table-acl'])

    if protocols_flavor().service_map():
      args.extend(['-service_map', ','.join(protocols_flavor().service_map())])
    if self.grpc_enabled():
      args.extend(['-grpc_port', str(self.grpc_port)])
    if lameduck_period:
      args.extend(['-lameduck-period', lameduck_period])
    if security_policy:
      args.extend(['-security_policy', security_policy])
    if extra_args:
      args.extend(extra_args)

    args.extend(['-enable-autocommit'])
    stderr_fd = open(
        os.path.join(environment.vtlogroot, 'vttablet-%d.stderr' %
                     self.tablet_uid), 'w')
    # increment count only the first time
    if not self.proc:
      Tablet.tablets_running += 1
    self.proc = utils.run_bg(args, stderr=stderr_fd, extra_env=extra_env)

    log_message = (
        'Started vttablet: %s (%s) with pid: %s - Log files: '
        '%s/vttablet.*.{INFO,WARNING,ERROR,FATAL}.*.%s' %
        (self.tablet_uid, self.tablet_alias, self.proc.pid,
         environment.vtlogroot, self.proc.pid))
    # This may race with the stderr output from the process (though
    # that's usually empty).
    stderr_fd.write(log_message + '\n')
    stderr_fd.close()
    logging.debug(log_message)

    # wait for query service to be in the right state
    if wait_for_state:
      self.wait_for_vttablet_state(wait_for_state, port=port)

    return self.proc

  def wait_for_vttablet_state(self, expected, timeout=60.0, port=None):
    expr = re.compile('^' + expected + '$')
    while True:
      v = utils.get_vars(port or self.port)
      last_seen_state = '?'
      if v is None:
        if self.proc.poll() is not None:
          raise utils.TestError(
              'vttablet died while test waiting for state %s' % expected)
        logging.debug(
            '  vttablet %s not answering at /debug/vars, waiting...',
            self.tablet_alias)
      else:
        if 'TabletStateName' not in v:
          logging.debug(
              '  vttablet %s not exporting TabletStateName, waiting...',
              self.tablet_alias)
        else:
          s = v['TabletStateName']
          last_seen_state = s
          if expr.match(s):
            break
          else:
            logging.debug(
                '  vttablet %s in state %s != %s', self.tablet_alias, s,
                expected)
      timeout = utils.wait_step(
          'waiting for state %s (last seen state: %s)' %
          (expected, last_seen_state),
          timeout, sleep_time=0.1)

  def wait_for_mysqlctl_socket(self, timeout=30.0):
    mysql_sock = os.path.join(self.tablet_dir, 'mysql.sock')
    mysqlctl_sock = os.path.join(self.tablet_dir, 'mysqlctl.sock')
    while True:
      if os.path.exists(mysql_sock) and os.path.exists(mysqlctl_sock):
        return
      timeout = utils.wait_step(
          'waiting for mysql and mysqlctl socket files: %s %s' %
          (mysql_sock, mysqlctl_sock), timeout)

  def _add_dbconfigs(self, args, repl_extra_flags=None):
    if repl_extra_flags is None:
      repl_extra_flags = {}
    config = dict(self.default_db_config)
    if self.keyspace:
      config['app']['dbname'] = self.dbname
      config['repl']['dbname'] = self.dbname
    config['repl'].update(repl_extra_flags)
    for key1 in config:
      for key2 in config[key1]:
        args.extend(['-db-config-' + key1 + '-' + key2, config[key1][key2]])

  def get_status(self):
    return utils.get_status(self.port)

  def get_healthz(self):
    return urllib2.urlopen('http://localhost:%d/healthz' % self.port).read()

  def kill_vttablet(self, wait=True):
    logging.debug('killing vttablet: %s, wait: %s', self.tablet_alias,
                  str(wait))
    if self.proc is not None:
      Tablet.tablets_running -= 1
      if self.proc.poll() is None:
        self.proc.terminate()
        if wait:
          self.proc.wait()
      self.proc = None

  def hard_kill_vttablet(self):
    logging.debug('hard killing vttablet: %s', self.tablet_alias)
    if self.proc is not None:
      Tablet.tablets_running -= 1
      if self.proc.poll() is None:
        self.proc.kill()
        self.proc.wait()
      self.proc = None

  def wait_for_binlog_server_state(self, expected, timeout=30.0):
    while True:
      v = utils.get_vars(self.port)
      if v == None:
        if self.proc.poll() is not None:
          raise utils.TestError(
              'vttablet died while test waiting for binlog state %s' %
              expected)
        logging.debug('  vttablet not answering at /debug/vars, waiting...')
      else:
        if 'UpdateStreamState' not in v:
          logging.debug(
              '  vttablet not exporting BinlogServerState, waiting...')
        else:
          s = v['UpdateStreamState']
          if s != expected:
            logging.debug("  vttablet's binlog server in state %s != %s", s,
                          expected)
          else:
            break
      timeout = utils.wait_step(
          'waiting for binlog server state %s' % expected,
          timeout, sleep_time=0.5)
    logging.debug('tablet %s binlog service is in state %s',
                  self.tablet_alias, expected)

  def wait_for_binlog_player_count(self, expected, timeout=30.0):
    while True:
      v = utils.get_vars(self.port)
      if v == None:
        if self.proc.poll() is not None:
          raise utils.TestError(
              'vttablet died while test waiting for binlog count %s' %
              expected)
        logging.debug('  vttablet not answering at /debug/vars, waiting...')
      else:
        if 'BinlogPlayerMapSize' not in v:
          logging.debug(
              '  vttablet not exporting BinlogPlayerMapSize, waiting...')
        else:
          s = v['BinlogPlayerMapSize']
          if s != expected:
            logging.debug("  vttablet's binlog player map has count %d != %d",
                          s, expected)
          else:
            break
      timeout = utils.wait_step(
          'waiting for binlog player count %d' % expected,
          timeout, sleep_time=0.5)
    logging.debug('tablet %s binlog player has %d players',
                  self.tablet_alias, expected)

  @classmethod
  def check_vttablet_count(klass):
    if Tablet.tablets_running > 0:
      raise utils.TestError('This test is not killing all its vttablets')

  def execute(self, sql, bindvars=None, transaction_id=None, auto_log=True):
    """execute uses 'vtctl VtTabletExecute' to execute a command.
    """
    args = [
        'VtTabletExecute',
        '-keyspace', self.keyspace,
        '-shard', self.shard,
    ]
    if bindvars:
      args.extend(['-bind_variables', json.dumps(bindvars)])
    if transaction_id:
      args.extend(['-transaction_id', str(transaction_id)])
    args.extend([self.tablet_alias, sql])
    return utils.run_vtctl_json(args, auto_log=auto_log)

  def begin(self, auto_log=True):
    """begin uses 'vtctl VtTabletBegin' to start a transaction.
    """
    args = [
        'VtTabletBegin',
        '-keyspace', self.keyspace,
        '-shard', self.shard,
        self.tablet_alias,
    ]
    result = utils.run_vtctl_json(args, auto_log=auto_log)
    return result['transaction_id']

  def commit(self, transaction_id, auto_log=True):
    """commit uses 'vtctl VtTabletCommit' to commit a transaction.
    """
    args = [
        'VtTabletCommit',
        '-keyspace', self.keyspace,
        '-shard', self.shard,
        self.tablet_alias,
        str(transaction_id),
    ]
    return utils.run_vtctl(args, auto_log=auto_log)

  def rollback(self, transaction_id, auto_log=True):
    """rollback uses 'vtctl VtTabletRollback' to rollback a transaction.
    """
    args = [
        'VtTabletRollback',
        '-keyspace', self.keyspace,
        '-shard', self.shard,
        self.tablet_alias,
        str(transaction_id),
    ]
    return utils.run_vtctl(args, auto_log=auto_log)


def kill_tablets(tablets):
  for t in tablets:
    logging.debug('killing vttablet: %s', t.tablet_alias)
    if t.proc is not None:
      Tablet.tablets_running -= 1
      t.proc.terminate()

  for t in tablets:
    if t.proc is not None:
      t.proc.wait()
      t.proc = None
