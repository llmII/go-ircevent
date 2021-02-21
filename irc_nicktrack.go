package irc

import (
	"strings"
	"sync"
)

// Channel stores the channel information for a channel this Connection is a member of
type Channel struct {
	sync.Mutex
	// We should extract topic by handling the correct Event at some point in the future!
	Topic string
	// We should extract channel modes by handling the correct Event at some point in the future!
	Mode string
	// leaving exposed for now, will be unexported in the future!
	Users map[string]User
}

func (ch *Channel) GetUser(name string) (User, bool) {
	ch.Lock()
	defer ch.Unlock()
	ret, ok := ch.Users[name]
	return ret, ok
}

func (ch *Channel) IterUsers(call func(string, User)) {
	ch.Lock()
	defer ch.Unlock()
	for name, user := range ch.Users {
		call(name, user)
	}
}

// User stores information on an IRC user encountered by this Connection
type User struct {
	// FIXME: If we support Host we should set up a WHO handler
	// Careful though - some IRCd's (looking at unreal) silently treat WHO as WHOX
	// and the resultant fields we expect to parse out could be out of order!
	Host string
	Mode string
}

// This checks by display mode, not by the mode sent in MODE commands (since those are valid
// letters for general usage in things MODE would apply upon)
func (irc *Connection) isModeChar(c rune) bool {
	_, ok := irc.PrefixModes().Display[c]
	return ok
}

func (irc *Connection) getOrCreateChannel(name string) *Channel {
	irc.channelsMutex.Lock()
	defer irc.channelsMutex.Unlock()
	if _, ok := irc.Channels[name]; !ok {
		irc.Channels[name] = &Channel{Users: make(map[string]User)}
	}
	return irc.Channels[name]
}

// GetChannel gets a channel by name that the Connection is on
func (irc *Connection) GetChannel(name string) (*Channel, bool) {
	irc.channelsMutex.Lock()
	defer irc.channelsMutex.Unlock()
	ret, ok := irc.Channels[name]
	return ret, ok
}

// IterChannels allows for calling code to provide a callable that is ran for each
// channel the Connection is on
func (irc *Connection) IterChannels(call func(string, *Channel)) {
	irc.channelsMutex.Lock()
	defer irc.channelsMutex.Unlock()
	for name, ch := range irc.Channels {
		ch.Lock()
		call(name, ch)
		ch.Unlock()
	}
}

// SetupNickTrack enables stateful tracking of IRC Users and Channels on this Connection
func (irc *Connection) SetupNickTrack() {
	// relies on ISUPPORT so introduce it
	irc.SetupFeatureDetect()
	// 353: RPL_NAMEREPLY per RFC1459
	// will typically receive this on channel joins and when NAMES is
	// called via GetNicksOnChan
	irc.AddCallback("353", func(e *Event) {

		// check if chan exists in map, if not make one
		channel := irc.getOrCreateChannel(e.Arguments[2])

		for _, modenick := range strings.Split(e.Message(), " ") {
			u := User{}
			idx := 0
			for pos, c := range modenick {
				if !irc.isModeChar(c) {
					idx = pos
					break
				}
			}

			if idx > 0 {
				u.Mode = "+"
				for _, mc := range modenick[0:idx] {
					u.Mode += string(irc.PrefixModes().Display[mc])
				}
			}

			channel.Lock()
			channel.Users[modenick[idx:]] = u
			channel.Unlock()
		}
	})

	// FIXME: I don't handle multiple modes in the same Event!!!
	irc.AddCallback("MODE", func(e *Event) {
		if len(e.Arguments) == 3 { // 3 == for channel 2 == for user on server
			channel := irc.getOrCreateChannel(e.Arguments[0])
			channel.Lock()
			defer channel.Unlock()

			if _, ok := channel.Users[e.Arguments[2]]; ok != true {
				channel.Users[e.Arguments[2]] = User{Mode: e.Arguments[1]}
			} else {
				u := channel.Users[e.Arguments[2]]
				u.Mode = e.Arguments[1]
				channel.Users[e.Arguments[2]] = u
			}
		}
	})

	// IRC doesn't report Channels when a nick is changed, so we have to figure this out
	// based on prior acquired tracking information
	// Maybe a User should keep a reference to all the channels they're in?
	irc.AddCallback("NICK", func(e *Event) {
		if len(e.Arguments) == 1 { // Sanity check
			irc.channelsMutex.Lock()
			defer irc.channelsMutex.Unlock()
			for _, ch := range irc.Channels {
				ch.Lock()
				if _, ok := ch.Users[e.Nick]; ok {
					u := ch.Users[e.Nick]
					u.Host = e.Host
					ch.Users[e.Arguments[0]] = u
					delete(ch.Users, e.Nick)
				}
				ch.Unlock()
			}
		}
	})

	irc.AddCallback("JOIN", func(e *Event) {
		channel := irc.getOrCreateChannel(e.Arguments[0])
		channel.Lock()
		defer channel.Unlock()
		channel.Users[e.Nick] = User{Host: e.Source}
	})

	irc.AddCallback("PART", func(e *Event) {
		channel := irc.getOrCreateChannel(e.Arguments[0])
		channel.Lock()
		defer channel.Unlock()
		delete(channel.Users, e.Nick)
	})

	irc.AddCallback("QUIT", func(e *Event) {
		irc.channelsMutex.Lock()
		defer irc.channelsMutex.Unlock()
		for _, ch := range irc.Channels {
			ch.Lock()
			delete(ch.Users, e.Nick)
			ch.Unlock()
		}
	})
}