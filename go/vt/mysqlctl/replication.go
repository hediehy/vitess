// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Handle creating replicas and setting up the replication streams.
*/

package mysqlctl

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	log "github.com/golang/glog"
	"github.com/youtube/vitess/go/mysql"
	"github.com/youtube/vitess/go/netutil"
	"github.com/youtube/vitess/go/sqldb"
	"github.com/youtube/vitess/go/vt/dbconfigs"
	"github.com/youtube/vitess/go/vt/hook"
	"github.com/youtube/vitess/go/vt/mysqlctl/replication"
)

const (
	// SQLStartSlave is the SQl command issued to start MySQL replication
	SQLStartSlave = "START SLAVE"

	// SQLStopSlave is the SQl command issued to stop MySQL replication
	SQLStopSlave = "STOP SLAVE"
)

func fillStringTemplate(tmpl string, vars interface{}) (string, error) {
	myTemplate := template.Must(template.New("").Parse(tmpl))
	data := new(bytes.Buffer)
	if err := myTemplate.Execute(data, vars); err != nil {
		return "", err
	}
	return data.String(), nil
}

func changeMasterArgs(params *sqldb.ConnParams, masterHost string, masterPort int, masterConnectRetry int) []string {
	var args []string
	args = append(args, fmt.Sprintf("MASTER_HOST = '%s'", masterHost))
	args = append(args, fmt.Sprintf("MASTER_PORT = %d", masterPort))
	args = append(args, fmt.Sprintf("MASTER_USER = '%s'", params.Uname))
	args = append(args, fmt.Sprintf("MASTER_PASSWORD = '%s'", params.Pass))
	args = append(args, fmt.Sprintf("MASTER_CONNECT_RETRY = %d", masterConnectRetry))

	if mysql.SslEnabled(params) {
		args = append(args, "MASTER_SSL = 1")
	}
	if params.SslCa != "" {
		args = append(args, fmt.Sprintf("MASTER_SSL_CA = '%s'", params.SslCa))
	}
	if params.SslCaPath != "" {
		args = append(args, fmt.Sprintf("MASTER_SSL_CAPATH = '%s'", params.SslCaPath))
	}
	if params.SslCert != "" {
		args = append(args, fmt.Sprintf("MASTER_SSL_CERT = '%s'", params.SslCert))
	}
	if params.SslKey != "" {
		args = append(args, fmt.Sprintf("MASTER_SSL_KEY = '%s'", params.SslKey))
	}
	return args
}

// parseSlaveStatus parses the common fields of SHOW SLAVE STATUS.
func parseSlaveStatus(fields map[string]string) replication.Status {
	status := replication.Status{
		MasterHost:      fields["Master_Host"],
		SlaveIORunning:  fields["Slave_IO_Running"] == "Yes",
		SlaveSQLRunning: fields["Slave_SQL_Running"] == "Yes",
	}
	parseInt, _ := strconv.ParseInt(fields["Master_Port"], 10, 0)
	status.MasterPort = int(parseInt)
	parseInt, _ = strconv.ParseInt(fields["Connect_Retry"], 10, 0)
	status.MasterConnectRetry = int(parseInt)
	parseUint, _ := strconv.ParseUint(fields["Seconds_Behind_Master"], 10, 0)
	status.SecondsBehindMaster = uint(parseUint)
	return status
}

// WaitForSlaveStart waits until the deadline for replication to start.
// This validates the current master is correct and can be connected to.
func WaitForSlaveStart(mysqld MysqlDaemon, slaveStartDeadline int) error {
	var rowMap map[string]string
	for slaveWait := 0; slaveWait < slaveStartDeadline; slaveWait++ {
		status, err := mysqld.SlaveStatus()
		if err != nil {
			return err
		}

		if status.SlaveRunning() {
			return nil
		}
		time.Sleep(time.Second)
	}

	errorKeys := []string{"Last_Error", "Last_IO_Error", "Last_SQL_Error"}
	errs := make([]string, 0, len(errorKeys))
	for _, key := range errorKeys {
		if rowMap[key] != "" {
			errs = append(errs, key+": "+rowMap[key])
		}
	}
	if len(errs) != 0 {
		return errors.New(strings.Join(errs, ", "))
	}
	return nil
}

// StartSlave starts a slave on the provided MysqldDaemon
func StartSlave(md MysqlDaemon, hookExtraEnv map[string]string) error {
	if err := md.ExecuteSuperQueryList([]string{SQLStartSlave}); err != nil {
		return err
	}

	h := hook.NewSimpleHook("postflight_start_slave")
	h.ExtraEnv = hookExtraEnv
	return h.ExecuteOptional()
}

// StopSlave stops a slave on the provided MysqldDaemon
func StopSlave(md MysqlDaemon, hookExtraEnv map[string]string) error {
	h := hook.NewSimpleHook("preflight_stop_slave")
	h.ExtraEnv = hookExtraEnv
	if err := h.ExecuteOptional(); err != nil {
		return err
	}

	return md.ExecuteSuperQueryList([]string{SQLStopSlave})
}

// GetMysqlPort returns mysql port
func (mysqld *Mysqld) GetMysqlPort() (int32, error) {
	qr, err := mysqld.FetchSuperQuery("SHOW VARIABLES LIKE 'port'")
	if err != nil {
		return 0, err
	}
	if len(qr.Rows) != 1 {
		return 0, errors.New("no port variable in mysql")
	}
	utemp, err := strconv.ParseUint(qr.Rows[0][1].String(), 10, 16)
	if err != nil {
		return 0, err
	}
	return int32(utemp), nil
}

// IsReadOnly return true if the instance is read only
func (mysqld *Mysqld) IsReadOnly() (bool, error) {
	qr, err := mysqld.FetchSuperQuery("SHOW VARIABLES LIKE 'read_only'")
	if err != nil {
		return true, err
	}
	if len(qr.Rows) != 1 {
		return true, errors.New("no read_only variable in mysql")
	}
	if qr.Rows[0][1].String() == "ON" {
		return true, nil
	}
	return false, nil
}

// SetReadOnly set/unset the read_only flag
func (mysqld *Mysqld) SetReadOnly(on bool) error {
	query := "SET GLOBAL read_only = "
	if on {
		query += "ON"
	} else {
		query += "OFF"
	}
	return mysqld.ExecuteSuperQuery(query)
}

var (
	// ErrNotSlave means there is no slave status
	ErrNotSlave = errors.New("no slave status")
	// ErrNotMaster means there is no master status
	ErrNotMaster = errors.New("no master status")
)

// WaitMasterPos lets slaves wait to given replication position
func (mysqld *Mysqld) WaitMasterPos(targetPos replication.Position, waitTimeout time.Duration) error {
	flavor, err := mysqld.flavor()
	if err != nil {
		return fmt.Errorf("WaitMasterPos needs flavor: %v", err)
	}
	return flavor.WaitMasterPos(mysqld, targetPos, waitTimeout)
}

// SlaveStatus returns the slave replication statuses
func (mysqld *Mysqld) SlaveStatus() (replication.Status, error) {
	flavor, err := mysqld.flavor()
	if err != nil {
		return replication.Status{}, fmt.Errorf("SlaveStatus needs flavor: %v", err)
	}
	return flavor.SlaveStatus(mysqld)
}

// MasterPosition returns master replication position
func (mysqld *Mysqld) MasterPosition() (rp replication.Position, err error) {
	flavor, err := mysqld.flavor()
	if err != nil {
		return rp, fmt.Errorf("MasterPosition needs flavor: %v", err)
	}
	return flavor.MasterPosition(mysqld)
}

// SetSlavePositionCommands returns the commands to set the
// replication position at which the slave will resume
// when it is later reparented with SetMasterCommands.
func (mysqld *Mysqld) SetSlavePositionCommands(pos replication.Position) ([]string, error) {
	flavor, err := mysqld.flavor()
	if err != nil {
		return nil, fmt.Errorf("SetSlavePositionCommands needs flavor: %v", err)
	}
	return flavor.SetSlavePositionCommands(pos)
}

// SetMasterCommands returns the commands to run to make the provided
// host / port the master.
func (mysqld *Mysqld) SetMasterCommands(masterHost string, masterPort int) ([]string, error) {
	flavor, err := mysqld.flavor()
	if err != nil {
		return nil, fmt.Errorf("SetMasterCommands needs flavor: %v", err)
	}
	params, err := dbconfigs.MysqlParams(mysqld.replParams)
	if err != nil {
		return nil, err
	}
	return flavor.SetMasterCommands(&params, masterHost, masterPort, int(masterConnectRetry.Seconds()))
}

// ResetReplicationCommands returns the commands to run to reset all
// replication for this host.
func (mysqld *Mysqld) ResetReplicationCommands() ([]string, error) {
	flavor, err := mysqld.flavor()
	if err != nil {
		return nil, fmt.Errorf("ResetReplicationCommands needs flavor: %v", err)
	}
	return flavor.ResetReplicationCommands(), nil
}

// +------+---------+---------------------+------+-------------+------+----------------------------------------------------------------+------------------+
// | Id   | User    | Host                | db   | Command     | Time | State                                                          | Info             |
// +------+---------+---------------------+------+-------------+------+----------------------------------------------------------------+------------------+
// | 9792 | vt_repl | host:port           | NULL | Binlog Dump |   54 | Has sent all binlog to slave; waiting for binlog to be updated | NULL             |
// | 9797 | vt_dba  | localhost           | NULL | Query       |    0 | NULL                                                           | show processlist |
// +------+---------+---------------------+------+-------------+------+----------------------------------------------------------------+------------------+
//
// Array indices for the results of SHOW PROCESSLIST.
const (
	colConnectionID = iota
	colUsername
	colClientAddr
	colDbName
	colCommand
)

const (
	// this is the command used by mysql slaves
	binlogDumpCommand = "Binlog Dump"
)

// FindSlaves gets IP addresses for all currently connected slaves.
func FindSlaves(mysqld MysqlDaemon) ([]string, error) {
	qr, err := mysqld.FetchSuperQuery("SHOW PROCESSLIST")
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, 32)
	for _, row := range qr.Rows {
		// Check for prefix, since it could be "Binlog Dump GTID".
		if strings.HasPrefix(row[colCommand].String(), binlogDumpCommand) {
			host, _, err := netutil.SplitHostPort(row[colClientAddr].String())
			if err != nil {
				return nil, fmt.Errorf("FindSlaves: malformed addr %v", err)
			}
			addrs = append(addrs, host)
		}
	}

	return addrs, nil
}

// WaitBlpPosition will wait for the filtered replication to reach at least
// the provided position.
func WaitBlpPosition(mysqld MysqlDaemon, sql string, replicationPosition string, waitTimeout time.Duration) error {
	position, err := replication.DecodePosition(replicationPosition)
	if err != nil {
		return err
	}
	timeOut := time.Now().Add(waitTimeout)
	for {
		if time.Now().After(timeOut) {
			break
		}

		qr, err := mysqld.FetchSuperQuery(sql)
		if err != nil {
			return err
		}
		if len(qr.Rows) != 1 {
			return fmt.Errorf("QueryBlpCheckpoint(%v) returned unexpected row count: %v", sql, len(qr.Rows))
		}
		var pos replication.Position
		if !qr.Rows[0][0].IsNull() {
			pos, err = replication.DecodePosition(qr.Rows[0][0].String())
			if err != nil {
				return err
			}
		}
		if pos.AtLeast(position) {
			return nil
		}

		log.Infof("Sleeping 1 second waiting for binlog replication(%v) to catch up: %v != %v", sql, pos, position)
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("WaitBlpPosition(%v) timed out", sql)
}

// EnableBinlogPlayback prepares the server to play back events from a binlog stream.
// Whatever it does for a given flavor, it must be idempotent.
func (mysqld *Mysqld) EnableBinlogPlayback() error {
	flavor, err := mysqld.flavor()
	if err != nil {
		return fmt.Errorf("EnableBinlogPlayback needs flavor: %v", err)
	}
	return flavor.EnableBinlogPlayback(mysqld)
}

// DisableBinlogPlayback returns the server to the normal state after streaming.
// Whatever it does for a given flavor, it must be idempotent.
func (mysqld *Mysqld) DisableBinlogPlayback() error {
	flavor, err := mysqld.flavor()
	if err != nil {
		return fmt.Errorf("DisableBinlogPlayback needs flavor: %v", err)
	}
	return flavor.DisableBinlogPlayback(mysqld)
}
