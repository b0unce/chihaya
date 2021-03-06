// Copyright 2013 The Chihaya Authors. All rights reserved.
// Use of this source code is governed by the BSD 2-Clause license,
// which can be found in the LICENSE file.

package mock

import (
	"sync"

	"github.com/chihaya/chihaya/storage"
	"github.com/chihaya/chihaya/storage/tracker"
)

type Pool struct {
	users  map[string]*storage.User
	usersM sync.RWMutex

	torrents  map[string]*storage.Torrent
	torrentsM sync.RWMutex

	whitelist  map[string]bool
	whitelistM sync.RWMutex
}

func (p *Pool) Get() (tracker.Conn, error) {
	return &Conn{
		Pool: p,
	}, nil
}

func (p *Pool) Close() error {
	return nil
}
