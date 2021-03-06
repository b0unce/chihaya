// Copyright 2013 The Chihaya Authors. All rights reserved.
// Use of this source code is governed by the BSD 2-Clause license,
// which can be found in the LICENSE file.

// Package storage implements the models for an abstraction over the
// multiple data stores used by a BitTorrent tracker.
package storage

import (
	"strconv"
)

// Peer is the internal representation of a participant in a swarm.
type Peer struct {
	ID        string `json:"id"`
	UserID    uint64 `json:"user_id"`
	TorrentID uint64 `json:"torrent_id"`

	IP   string `json:"ip"`
	Port uint64 `json:"port"`

	Uploaded     uint64 `json:"uploaded"`
	Downloaded   uint64 `json:"downloaded`
	Left         uint64 `json:"left"`
	LastAnnounce int64  `json:"last_announce"`
}

// PeerMapKey is a helper that returns the proper format for keys used for maps
// of peers (i.e. torrent.Seeders & torrent.Leechers).
func PeerMapKey(peer *Peer) string {
	return peer.ID + ":" + strconv.FormatUint(peer.UserID, 36)
}

// Torrent is the internal representation of a swarm for a given torrent file.
type Torrent struct {
	ID       uint64 `json:"id"`
	Infohash string `json:"infohash"`
	Active   bool   `json:"active"`

	Seeders  map[string]Peer `json:"seeders"`
	Leechers map[string]Peer `json:"leechers"`

	Snatches       uint64  `json:"snatches"`
	UpMultiplier   float64 `json:"up_multiplier"`
	DownMultiplier float64 `json:"down_multiplier"`
	LastAction     int64   `json:"last_action"`
}

// User is the internal representation of registered user for private trackers.
type User struct {
	ID      uint64 `json:"id"`
	Passkey string `json:"passkey"`

	UpMultiplier   float64 `json:"up_multiplier"`
	DownMultiplier float64 `json:"down_multiplier"`
	Snatches       uint64  `json:"snatches"`
}
