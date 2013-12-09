// Copyright 2013 The Chihaya Authors. All rights reserved.
// Use of this source code is governed by the BSD 2-Clause license,
// which can be found in the LICENSE file.

package server

import (
	"errors"
	"log"
	"net"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/chihaya/chihaya/storage"
)

func (s Server) serveAnnounce(w http.ResponseWriter, r *http.Request) {
	// Parse the required parameters off of a query
	compact, numWant, infohash, peerID, event, ip, port, uploaded, downloaded, left, err := s.validateAnnounceQuery(r)
	if err != nil {
		fail(err, w, r)
		return
	}

	// Get a connection to the tracker db
	conn, err := s.dbConnPool.Get()
	if err != nil {
		log.Panicf("server: %s", err)
	}

	// Validate the user's passkey
	passkey, _ := path.Split(r.URL.Path)
	user, err := validateUser(conn, passkey)
	if err != nil {
		fail(err, w, r)
		return
	}

	// Check if the user's client is whitelisted
	whitelisted, err := conn.ClientWhitelisted(parsePeerID(peerID))
	if err != nil {
		log.Panicf("server: %s", err)
	}
	if !whitelisted {
		fail(errors.New("Your client is not approved"), w, r)
		return
	}

	// Find the specified torrent
	torrent, exists, err := conn.FindTorrent(infohash)
	if err != nil {
		log.Panicf("server: %s", err)
	}
	if !exists {
		fail(errors.New("This torrent does not exist"), w, r)
		return
	}

	// If the torrent was pruned and the user is seeding, unprune it
	if !torrent.Active && left == 0 {
		err := conn.MarkActive(torrent)
		if err != nil {
			log.Panicf("server: %s", err)
		}
	}

	// Create a new peer object from the request
	peer := &storage.Peer{
		ID:           peerID,
		UserID:       user.ID,
		TorrentID:    torrent.ID,
		IP:           ip,
		Port:         port,
		Uploaded:     uploaded,
		Downloaded:   downloaded,
		Left:         left,
		LastAnnounce: time.Now().Unix(),
	}

	// Look for the user in in the pool of seeders and leechers
	_, seeder := torrent.Seeders[storage.PeerMapKey(peer)]
	_, leecher := torrent.Leechers[storage.PeerMapKey(peer)]

	switch {
	// Guarantee that no user is in both pools
	case seeder && leecher:
		if left == 0 {
			err := conn.RemoveLeecher(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
			leecher = false
		} else {
			err := conn.RemoveSeeder(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
			seeder = false
		}

	case seeder:
		// Update the peer with the stats from the request
		err := conn.SetSeeder(torrent, peer)
		if err != nil {
			log.Panicf("server: %s", err)
		}

	case leecher:
		// Update the peer with the stats from the request
		err := conn.SetLeecher(torrent, peer)
		if err != nil {
			log.Panicf("server: %s", err)
		}

	default:
		if left == 0 {
			// Save the peer as a new seeder
			err := conn.AddSeeder(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
		} else {
			err = conn.AddLeecher(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
		}
	}

	// Handle any events in the request
	switch {
	case event == "stopped" || event == "paused":
		if seeder {
			err := conn.RemoveSeeder(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
		}
		if leecher {
			err := conn.RemoveLeecher(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
		}

	case event == "completed":
		err := conn.RecordSnatch(user, torrent)
		if err != nil {
			log.Panicf("server: %s", err)
		}
		if leecher {
			err := conn.LeecherFinished(torrent, peer)
			if err != nil {
				log.Panicf("server: %s", err)
			}
		}

	case leecher && left == 0:
		// A leecher completed but the event was never received
		err := conn.LeecherFinished(torrent, peer)
		if err != nil {
			log.Panicf("server: %s", err)
		}
	}

	if ip != peer.IP || port != peer.Port {
		peer.Port = port
		peer.IP = ip
	}

	// Generate the response
	seedCount := len(torrent.Seeders)
	leechCount := len(torrent.Leechers)

	writeBencoded(w, "d")
	writeBencoded(w, "complete")
	writeBencoded(w, seedCount)
	writeBencoded(w, "incomplete")
	writeBencoded(w, leechCount)
	writeBencoded(w, "interval")
	writeBencoded(w, s.conf.Announce.Duration)
	writeBencoded(w, "min interval")
	writeBencoded(w, s.conf.MinAnnounce.Duration)

	if numWant > 0 && event != "stopped" && event != "paused" {
		writeBencoded(w, "peers")
		var peerCount, count int

		if compact {
			if left > 0 {
				peerCount = minInt(numWant, leechCount)
			} else {
				peerCount = minInt(numWant, leechCount+seedCount-1)
			}
			writeBencoded(w, strconv.Itoa(peerCount*6))
			writeBencoded(w, ":")
		} else {
			writeBencoded(w, "l")
		}

		if left > 0 {
			// If they're seeding, give them only leechers
			count += writeLeechers(w, user, torrent, numWant, compact)
		} else {
			// If they're leeching, prioritize giving them seeders
			count += writeSeeders(w, user, torrent, numWant, compact)
			count += writeLeechers(w, user, torrent, numWant - count, compact)
		}

		if compact && peerCount != count {
			log.Panicf("Calculated peer count (%d) != real count (%d)", peerCount, count)
		}

		if !compact {
			writeBencoded(w, "e")
		}
	}
	writeBencoded(w, "e")
}

func (s Server) validateAnnounceQuery(r *http.Request) (compact bool, numWant int, infohash, peerID, event, ip string, port, uploaded, downloaded, left uint64, err error) {
	pq, err := parseQuery(r.URL.RawQuery)
	if err != nil {
		return false, 0, "", "", "", "", 0, 0, 0, 0, err
	}

	compact = pq.Params["compact"] == "1"
	numWant = requestedPeerCount(s.conf.DefaultNumWant, pq)
	infohash, _ = pq.Params["info_hash"]
	peerID, _ = pq.Params["peer_id"]
	event, _ = pq.Params["event"]
	ip, _ = requestedIP(r, pq)
	port, portErr := pq.getUint64("port")
	uploaded, uploadedErr := pq.getUint64("uploaded")
	downloaded, downloadedErr := pq.getUint64("downloaded")
	left, leftErr := pq.getUint64("left")

	if infohash == "" ||
		peerID == "" ||
		ip == "" ||
		portErr != nil ||
		uploadedErr != nil ||
		downloadedErr != nil ||
		leftErr != nil {
		return false, 0, "", "", "", "", 0, 0, 0, 0, errors.New("Malformed request")
	}
	return
}

func requestedPeerCount(fallback int, pq *parsedQuery) int {
	if numWantStr, exists := pq.Params["numWant"]; exists {
		numWant, err := strconv.Atoi(numWantStr)
		if err != nil {
			return fallback
		}
		return numWant
	}
	return fallback
}

func requestedIP(r *http.Request, pq *parsedQuery) (string, error) {
	ip := ""
	
	// TODO: Make this a configurable option as it allows peers to set their peer address
	if p_ip, ok := pq.Params["ip"]; ok {
		ip = p_ip
	}
	if p_ipv4, ok := pq.Params["ipv4"]; ok {
		ip = p_ipv4
        }
	// TODO: Make this a configurable option as above but for load balancers instead
	if xRealIp := r.Header.Get("X-Real-Ip"); xRealIp != "" {
		ip = xRealIp
	}

	// If the IP has not matched the user or proxy provided headers then use the socket information
	if ip == "" {
			portIndex := len(r.RemoteAddr) - 1
			for ; portIndex >= 0; portIndex-- {
					if r.RemoteAddr[portIndex] == ':' {
							break
					}
			}
			if portIndex != -1 {
					ip = r.RemoteAddr[0:portIndex]
			}
	}
	
	// Validate that what we have is an actual IP address
	if ip := net.ParseIP(ip); ip != nil {
			fmt.Print("%s\n", ip)
			return ip.String(), nil
	} else {
			return "", errors.New("Failed to parse IP address")
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func writeSeeders(w http.ResponseWriter, user *storage.User, t *storage.Torrent, numWant int, compact bool) int {
	count := 0
	for _, peer := range t.Seeders {
		if count >= numWant {
			break
		}
		if peer.UserID == user.ID {
			continue
		}
		if compact {
			// TODO writeBencoded(w, compactAddr)
		} else {
			writeBencoded(w, "d")
			writeBencoded(w, "ip")
			writeBencoded(w, peer.IP)
			writeBencoded(w, "peer id")
			writeBencoded(w, peer.ID)
			writeBencoded(w, "port")
			writeBencoded(w, peer.Port)
			writeBencoded(w, "e")
		}
		count++
	}
	return count
}

func writeLeechers(w http.ResponseWriter, user *storage.User, t *storage.Torrent, numWant int, compact bool) int {
	count := 0
	for _, peer := range t.Leechers {
		if count >= numWant {
			break
		}
		if peer.UserID == user.ID {
			continue
		}
		if compact {
			// TODO writeBencoded(w, compactAddr)
		} else {
			writeBencoded(w, "d")
			writeBencoded(w, "ip")
			writeBencoded(w, peer.IP)
			writeBencoded(w, "peer id")
			writeBencoded(w, peer.ID)
			writeBencoded(w, "port")
			writeBencoded(w, peer.Port)
			writeBencoded(w, "e")
		}
		count++
	}
	return count
}
