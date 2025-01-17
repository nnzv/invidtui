package mediaplayer

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darkhz/invidtui/client"
	"github.com/darkhz/invidtui/utils"
	"github.com/darkhz/mpvipc"
)

// MPV describes the mpv player.
type MPV struct {
	socket  string
	monitor map[int]string

	lock sync.Mutex

	*mpvipc.Connection
}

var mpv MPV

// Init initializes and sets up MPV.
func (m *MPV) Init(execpath, ytdlpath, numretries, useragent, socket string) error {
	if err := m.connect(
		execpath, ytdlpath,
		numretries, useragent, socket,
	); err != nil {
		return err
	}

	m.monitor = make(map[int]string)

	go m.eventListener()
	go m.startMonitor()

	m.Call("keybind", "q", "")
	m.Call("keybind", "Ctrl+q", "")
	m.Call("keybind", "Shift+q", "")

	return nil
}

// Exit tells MPV to exit.
func (m *MPV) Exit() {
	m.Call("quit")
	os.Remove(m.socket)
}

// Exited returns whether MPV has exited or not.
func (m *MPV) Exited() bool {
	return m.Connection == nil || m.Connection.IsClosed()
}

// SendQuit sends a quit signal to the provided socket.
func (m *MPV) SendQuit(socket string) {
	conn := mpvipc.NewConnection(socket)
	if err := conn.Open(); err != nil {
		return
	}

	conn.Call("quit")
	time.Sleep(1 * time.Second)
}

// LoadFile loads the provided files into MPV. When more than one file is provided,
// the first file is treated as a video stream and the second file is attached as an audio stream.
func (m *MPV) LoadFile(title string, duration int64, audio bool, files ...string) error {
	options := "force-media-title=%" + strconv.Itoa(len(title)) + "%" + title

	if duration > 0 {
		options += ",length=" + strconv.FormatInt(duration, 10)
	}

	if audio {
		options += ",vid=no"
	}

	if len(files) == 2 {
		options += ",audio-file=" + files[1]
	}

	files[0] += "&options=" + url.QueryEscape(options)
	_, err := m.Call("loadfile", files[0], "append-play", options)
	if err != nil {
		return fmt.Errorf("MPV: Unable to load %s", title)
	}

	m.addToMonitor(title)

	return nil
}

// LoadPlaylist loads the provided playlist into MPV.
// If replace is true, the provided playlist will replace the current playing queue.
// renewLiveURL is a function to check and renew expired liev URLs in the playlist.
//
//gocyclo:ignore
func (m *MPV) LoadPlaylist(
	plpath string,
	replace bool,
	renewLiveURL func(uri string, audio bool) bool,
) error {
	var filesAdded int

	if replace {
		m.Call("playlist-clear")
		m.Call("playlist-remove", "current")

		m.clearMonitor()
	}

	pl, err := os.Open(plpath)
	if err != nil {
		return fmt.Errorf("MPV: Unable to open %s", plpath)
	}
	defer pl.Close()

	// We implement a simple playlist parser instead of relying on
	// the m3u8 package here, since that package deals with mainly
	// HLS playlists, and it seems to panic when certain EXTINF fields
	// are blank. With this method, we can parse the URLs from the playlist
	// directly, and pass the relevant options to mpv as well.
	scanner := bufio.NewScanner(pl)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		var title, options string

		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		lineURI, err := utils.IsValidURL(line)
		if err != nil {
			continue
		}

		lineURI.Host = utils.GetHostname(client.Instance())
		line = lineURI.String()

		data := lineURI.Query()
		if t := data.Get("title"); t != "" {
			title = t
		}
		if o := data.Get("options"); o != "" {
			options = replaceOptions(o)
		}
		if l := data.Get("length"); l == "Live" {
			audio := data.Get("mediatype") == "Audio"
			if renewed := renewLiveURL(line, audio); renewed {
				continue
			}
		}

		if !strings.Contains(options, "force-media-title") {
			options += ",force-media-title=%" + strconv.Itoa(len(title)) + "%" + title
		}

		if _, err := m.Call("loadfile", line, "append-play", options); err != nil {
			return err
		}

		filesAdded++

		m.addToMonitor(title)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	if filesAdded == 0 {
		return fmt.Errorf("MPV: No files were added")
	}

	return nil
}

// Title returns the title of the track located at 'pos'.
func (m *MPV) Title(pos int) string {
	pltitle, _ := m.Call("get_property_string", "playlist/"+strconv.Itoa(pos)+"/filename")

	if pltitle == nil {
		return "-"
	}

	return pltitle.(string)
}

// MediaType returns the mediatype of the currently playing track.
func (m *MPV) MediaType() string {
	_, err := m.Get("height")
	if err != nil {
		return "Audio"
	}

	return "Video"
}

// Play start the playback.
func (m *MPV) Play() {
	m.Set("pause", "no")
}

// Stop stops the playback.
func (m *MPV) Stop() {
	m.Call("stop")
}

// Next switches to the next track.
func (m *MPV) Next() {
	m.Call("playlist-next")
}

// Prev switches to the previous track.
func (m *MPV) Prev() {
	m.Call("playlist-prev")
}

// SeekForward seeks the track forward by 1s.
func (m *MPV) SeekForward() {
	m.Call("seek", 1)
}

// SeekBackward seeks the track backward by 1s.
func (m *MPV) SeekBackward() {
	m.Call("seek", -1)
}

// Position returns the seek position.
func (m *MPV) Position() int64 {
	timepos, err := m.Get("playback-time")
	if err != nil {
		return 0
	}

	return int64(timepos.(float64))
}

// Duration returns the total duration of the track.
func (m *MPV) Duration() int64 {
	duration, err := m.Get("duration")
	if err != nil {
		duration, err = m.Get("options/length")
		if err != nil {
			return 0
		}

		time, err := strconv.ParseInt(duration.(string), 10, 64)
		if err != nil {
			return 0
		}

		return time
	}

	return int64(duration.(float64))
}

// Paused returns whether playback is paused or not.
func (m *MPV) Paused() bool {
	paused, err := m.Get("pause")
	if err != nil {
		return false
	}

	return paused.(bool)
}

// TogglePaused toggles pausing the playback.
func (m *MPV) TogglePaused() {
	if m.Finished() && m.Paused() {
		m.Call("seek", 0, "absolute-percent")
	}

	m.Call("cycle", "pause")
}

// Shuffled returns whether tracks are shuffled.
func (m *MPV) Shuffled() bool {
	shuffle, err := m.Get("shuffle")
	if err != nil {
		return false
	}

	return shuffle.(bool)
}

// ToggleShuffled toggles shuffling of tracks.
func (m *MPV) ToggleShuffled() {
	m.Call("cycle", "shuffle")
}

// Muted returns whether playback is muted.
func (m *MPV) Muted() bool {
	mute, err := m.Get("mute")
	if err != nil {
		return false
	}

	return mute.(bool)
}

// ToggleMuted toggles muting of the playback.
func (m *MPV) ToggleMuted() {
	m.Call("cycle", "mute")
}

// LoopMode returns the current loop setting
// Either of loop-file (R-F), loop-playlist (R-P), or nothing.
func (m *MPV) LoopMode() string {
	lf, err := m.Call("get_property_string", "loop-file")
	if err != nil {
		return ""
	}

	lp, err := m.Call("get_property_string", "loop-playlist")
	if err != nil {
		return ""
	}

	if lf == "yes" || lf == "inf" {
		return "loop-file"
	}

	if lp == "yes" || lp == "inf" {
		return "loop-playlist"
	}

	return ""
}

// ToggleLoopMode toggles the loop mode between none, loop-file and loop-playlist.
func (m *MPV) ToggleLoopMode() {
	switch m.LoopMode() {
	case "":
		m.Set("loop-file", "yes")
		m.Set("loop-playlist", "no")

	case "loop-file":
		m.Set("loop-file", "no")
		m.Set("loop-playlist", "yes")

	case "loop-playlist":
		m.Set("loop-file", "no")
		m.Set("loop-playlist", "no")
	}
}

// Idle returns if the player is idle.
func (m *MPV) Idle() bool {
	idle, err := m.Get("core-idle")
	if err != nil {
		return false
	}

	return idle.(bool)
}

// Finished returns if the playback has finished.
func (m *MPV) Finished() bool {
	eof, err := m.Get("eof-reached")
	if err != nil {
		return false
	}

	return eof.(bool)
}

// Buffering returns if the player is buffering.
func (m *MPV) Buffering() bool {
	buf, err := m.Get("paused-for-cache")
	if err != nil {
		return true
	}

	return buf.(bool)
}

// Volume returns the volume.
func (m *MPV) Volume() int {
	vol, err := m.Get("volume")
	if err != nil {
		return -1
	}

	return int(vol.(float64))
}

// VolumeIncrease increments the volume by 1.
func (m *MPV) VolumeIncrease() {
	vol := m.Volume()
	if vol == -1 {
		return
	}

	m.Set("volume", vol+1)
}

// VolumeDecrease decreases the volume by 1.
func (m *MPV) VolumeDecrease() {
	vol := m.Volume()
	if vol == -1 {
		return
	}

	m.Set("volume", vol-1)
}

// QueueCount returns the total number of tracks within the queue.
func (m *MPV) QueueCount() int {
	count, err := m.Get("playlist-count")
	if err != nil {
		return 0
	}

	return int(count.(float64))
}

// QueuePosition returns the position of the current track within the queue.
func (m *MPV) QueuePosition() int {
	pos, err := m.Get("playlist-playing-pos")
	if err != nil {
		return 0
	}

	return int(pos.(float64))
}

// QueueDelete removes the track number from the queue.
func (m *MPV) QueueDelete(number int) {
	m.Call("playlist-remove", number)
}

// QueueMove moves the position of the track.
func (m *MPV) QueueMove(before, after int) {
	m.Call("playlist-move", after, before)
}

// QueueSwitchToTrack switches playback to the provided track number.
func (m *MPV) QueueSwitchToTrack(number int) {
	m.Set("playlist-pos", number)
}

// QueueData returns the current playlist data from MPV.
func (m *MPV) QueueData() string {
	list, err := m.Call("get_property_string", "playlist")
	if err != nil {
		return ""
	}

	return list.(string)
}

// QueuePlayLatest plays the latest track entry in the queue.
func (m *MPV) QueuePlayLatest() {
	m.Set("playlist-pos", m.QueueCount()-1)

	m.Play()
}

// QueueClear clears the queue.
func (m *MPV) QueueClear() {
	m.Call("playlist-clear")

	m.clearMonitor()
}

// WaitClosed waits for MPV to exit.
func (m *MPV) WaitClosed() {
	m.Connection.WaitUntilClosed()
}

// Call send a command to MPV.
func (m *MPV) Call(args ...interface{}) (interface{}, error) {
	if m.Exited() {
		return nil, fmt.Errorf("MPV: Connection closed")
	}

	return m.Connection.Call(args...)
}

// Get gets a property from the mpv instance.
func (m *MPV) Get(prop string) (interface{}, error) {
	if m.Exited() {
		return nil, fmt.Errorf("MPV: Connection closed")
	}

	return m.Connection.Get(prop)
}

// Set sets a property in the mpv instance.
func (m *MPV) Set(prop string, value interface{}) error {
	if m.Exited() {
		return fmt.Errorf("MPV: Connection closed")
	}

	return m.Connection.Set(prop, value)
}

// connect launches MPV and starts a new connection via the provided socket.
func (m *MPV) connect(mpvpath, ytdlpath, numretries, useragent, socket string) error {
	command := exec.Command(
		mpvpath,
		"--idle",
		"--keep-open",
		"--no-terminal",
		"--really-quiet",
		"--no-input-terminal",
		"--user-agent="+useragent,
		"--input-ipc-server="+socket,
		"--script-opts=ytdl_hook-ytdl_path="+ytdlpath,
	)

	if err := command.Start(); err != nil {
		return fmt.Errorf("MPV: Could not start")
	}

	conn := mpvipc.NewConnection(socket)
	retries, _ := strconv.Atoi(numretries)
	for i := 0; i <= retries; i++ {
		err := conn.Open()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		m.socket = socket
		m.Connection = conn

		return nil
	}

	return fmt.Errorf("MPV: Could not connect to socket")
}

// startMonitor starts monitoring MPV for error events.
func (m *MPV) startMonitor() {
	for id := range Events.ErrorNumber {
		m.lock.Lock()

		title := m.monitor[id]
		delete(m.monitor, id)

		m.lock.Unlock()

		select {
		case Events.ErrorEvent <- title:
		default:
		}
	}
}

// clearMonitor clears the error monitor.
func (m *MPV) clearMonitor() {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.monitor = make(map[int]string)
}

// addMonitor adds a track to be monitored for errors.
func (m *MPV) addToMonitor(title string) {
	select {
	case id := <-Events.FileNumber:
		m.lock.Lock()
		defer m.lock.Unlock()

		m.monitor[id] = title

	default:
	}
}

// eventListener listens for MPV events.
//
//gocyclo:ignore
func (m *MPV) eventListener() {
	events, stopListening := m.Connection.NewEventListener()

	defer m.Connection.Close()
	defer func() { stopListening <- struct{}{} }()

	m.Call("observe_property", 1, "playlist")

	//lint:ignore S1000 because for-range over the events channel blocks.
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}

			if event.ID == 1 {
				if data, ok := event.Data.([]interface{}); ok {
					pldata := make([]map[string]interface{}, len(data))

					for i, d := range data {
						if p, ok := d.(map[string]interface{}); ok {
							pldata[i] = p
						}
					}

					Events.DataEvent <- pldata

					break
				}
			}

			switch event.Name {
			case "start-file":
				m.Set("pause", "yes")
				m.Set("pause", "no")

				if len(event.ExtraData) > 0 {
					val := event.ExtraData["playlist_entry_id"]

					Events.FileNumber <- int(val.(float64))
				}

			case "end-file":
				if len(event.ExtraData) > 0 {
					err := event.ExtraData["file_error"]
					val := event.ExtraData["playlist_entry_id"]

					if err != nil && val != nil {
						if err.(string) != "" {
							Events.ErrorNumber <- int(val.(float64))
						}
					}
				}

			case "file-loaded":
				Events.FileLoadedEvent <- struct{}{}
			}
		}
	}
}
