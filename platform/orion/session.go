/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package orion

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/orion/driver"
	"github.com/pkg/errors"
)

type QueryIterator struct {
	it driver.QueryIterator
}

func (i *QueryIterator) Next() (string, []byte, uint64, uint64, bool, error) {
	return i.it.Next()
}

type SessionQueryExecutor struct {
	db     string
	dataTx driver.DataTx
	query  driver.Query
}

func (d *SessionQueryExecutor) Get(key string) ([]byte, error) {
	return d.dataTx.Get(d.db, key)
}

// GetDataByRange executes a range query. The startKey is
// inclusive but endKey is not. When the startKey is an empty string, it denotes
// `fetch keys from the beginning` while an empty endKey denotes `fetch keys till the
// the end`. The limit denotes the number of records to be fetched in total. However,
// when the limit is set to 0, it denotes no limit. The iterator returned by
// GetDataByRange is used to retrieve the records.
func (d *SessionQueryExecutor) GetDataByRange(startKey, endKey string, limit uint64) (*QueryIterator, error) {
	it, err := d.query.GetDataByRange(d.db, startKey, endKey, limit)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting data by range")
	}
	return &QueryIterator{it}, nil
}

type Session struct {
	s driver.Session
}

func (s *Session) QueryExecutor(db string) (*SessionQueryExecutor, error) {
	dataTx, err := s.s.DataTx("")
	if err != nil {
		return nil, errors.Wrapf(err, "failed creating data tx")
	}
	query, err := s.s.Query()
	if err != nil {
		return nil, errors.Wrapf(err, "failed creating query")
	}
	return &SessionQueryExecutor{dataTx: dataTx, db: db, query: query}, nil
}

func (s *Session) Ledger() (*Ledger, error) {
	ledger, err := s.s.Ledger()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get the ledger")
	}
	return &Ledger{l: ledger}, nil
}

// SessionManager is a session manager that allows the developer to access orion directly
type SessionManager struct {
	sm driver.SessionManager
}

// NewSession creates a new session to orion using the passed identity
func (sm *SessionManager) NewSession(id string) (*Session, error) {
	s, err := sm.sm.NewSession(id)
	if err != nil {
		return nil, err
	}
	return &Session{s: s}, nil
}
