package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/darkhz/invidtui/lib"
	"github.com/darkhz/tview"
	"github.com/gdamore/tcell/v2"
	"golang.org/x/sync/semaphore"
)

var (
	// Player displays the media player.
	Player *tview.Flex

	playerTitle *tview.TextView
	playerDesc  *tview.TextView
	playerChan  chan bool
	playing     bool
	playingLock sync.Mutex
	playerEvent chan struct{}

	addRateLimit *semaphore.Weighted
)

// SetupPlayer sets up a player view.
func SetupPlayer() {
	playerDesc = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)

	playerTitle = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)

	Player = tview.NewFlex().
		AddItem(playerTitle, 1, 0, false).
		AddItem(playerDesc, 1, 0, false).
		SetDirection(tview.FlexRow)

	Player.SetBackgroundColor(tcell.ColorDefault)
	playerTitle.SetBackgroundColor(tcell.ColorDefault)
	playerDesc.SetBackgroundColor(tcell.ColorDefault)

	playerChan = make(chan bool)
	playerEvent = make(chan struct{})

	addRateLimit = semaphore.NewWeighted(2)

	go StartPlayer()
	go monitorErrors()
}

// AddPlayer unhides the player view.
func AddPlayer() {
	if isPlaying() {
		return
	}

	SetPlayer(true)
	setPlaying(true)

	App.QueueUpdateDraw(func() {
		UIFlex.AddItem(Player, 2, 0, false)
	})
}

// RemovePlayer hides the player view and clears the playlist.
func RemovePlayer() {
	if !isPlaying() {
		return
	}

	SetPlayer(false)
	setPlaying(false)

	App.QueueUpdateDraw(func() {
		UIFlex.RemoveItem(Player)
	})

	lib.GetMPV().Stop()
	lib.GetMPV().PlaylistClear()
}

// StartPlayer starts the player loop, which gets the information
// on the currently playing file from mpv, sets the media title and
// displays the relevant information along with a progress bar.
func StartPlayer() {
	var pctx context.Context
	var pcancel context.CancelFunc

	for {
		play, ok := <-playerChan
		if !ok {
			return
		}

		if pctx != nil && !play {
			pcancel()
		}

		if !play {
			continue
		}

		pctx, pcancel = context.WithCancel(context.Background())

		go startPlayer(pctx, pcancel)
	}
}

// startPlayer is the player update loop.
func startPlayer(ctx context.Context, cancel context.CancelFunc) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()

	update := func() {
		var err error
		var width int
		var title, progressText string

		App.QueueUpdate(func() {
			_, _, width, _ = playerDesc.GetRect()
		})

		title, progressText, err = lib.GetProgress(width)
		if err != nil {
			cancel()
			return
		}

		App.QueueUpdateDraw(func() {
			playerDesc.SetText(progressText)
			playerTitle.SetText("[::b]" + tview.Escape(title))
		})
	}

	for {
		select {
		case <-ctx.Done():
			RemovePlayer()
			return

		case <-playerEvent:
			update()
			t.Reset(1 * time.Second)
			continue

		case <-t.C:
			update()
		}

	}
}

// StopPlayer finalizes the player before exit.
func StopPlayer() {
	SetPlayer(false)
	lib.GetMPV().MPVStop(true)
}

// SetPlayer sends a signal to StartPlayer on whether to
// start or stop the playback loop.
func SetPlayer(play bool) {
	playerChan <- play
}

// PlaySelected plays the current selection.
func PlaySelected(audio, current bool) {
	var media string

	info, err := getListReference()
	if err != nil {
		return
	}

	if audio {
		media = "audio"
	} else {
		media = "video"
	}

	if info.Type == "channel" {
		ErrorMessage(fmt.Errorf("Cannot play %s for channel type", media))
		return
	}

	InfoMessage("Loading "+media+" for "+info.Type+" "+info.Title, true)

	go func() {
		err := addRateLimit.Acquire(context.Background(), 1)
		if err != nil {
			return
		}
		defer addRateLimit.Release(1)

		switch info.Type {
		case "playlist":
			err = lib.LoadPlaylist(info.PlaylistID, audio)

		case "video":
			err = lib.LoadVideo(info.VideoID, audio)

		default:
			return
		}
		if err != nil {
			if err.Error() != "Rate-limit exceeded" {
				ErrorMessage(err)
			}

			return
		}

		AddPlayer()

		InfoMessage("Added "+info.Title, false)

		if current && info.Type == "video" {
			lib.GetMPV().PlaylistPlayLatest()
		}
	}()
}

// isPlaying returns the currently playing status.
func isPlaying() bool {
	playingLock.Lock()
	defer playingLock.Unlock()

	return playing
}

// setPlaying sets the new playing status.
func setPlaying(status bool) {
	playingLock.Lock()
	defer playingLock.Unlock()

	playing = status
}

// monitorErrors monitors for errors related to loading media
// from MPV.
func monitorErrors() {
	for {
		select {
		case msg, ok := <-lib.MPVErrors:
			if !ok {
				return
			}

			ErrorMessage(fmt.Errorf("Unable to play %s", msg))

		case _, ok := <-lib.MPVFileLoaded:
			if !ok {
				return
			}

			AddPlayer()
		}
	}
}

// capturePlayerEvent maps custom keybindings to the relevant
// mpv commands. This function is attached to ResultsList's InputCapture.
func capturePlayerEvent(event *tcell.EventKey) {
	captureSendPlayerEvent(event)

	switch event.Key() {
	case tcell.KeyCtrlO:
		ShowFileBrowser("Open playlist:", plOpenReplace, plFbExit)
	}

	switch event.Rune() {
	case 'a':
		PlaySelected(true, false)

	case 'v':
		PlaySelected(false, false)

	case 'A':
		PlaySelected(true, true)

	case 'V':
		PlaySelected(false, true)

	case 'p':
		playlistPopup()
	}
}

// captureSendPlayerEvent maps custom keybindings to
// the relevant mpv commands and sends a player event.
func captureSendPlayerEvent(event *tcell.EventKey) {
	var nokey, norune bool

	switch event.Key() {
	case tcell.KeyRight:
		lib.GetMPV().SeekForward()

	case tcell.KeyLeft:
		lib.GetMPV().SeekBackward()

	default:
		nokey = true
	}

	switch event.Rune() {
	case 'S':
		SetPlayer(false)

	case 'l':
		lib.GetMPV().CycleLoop()

	case 's':
		lib.GetMPV().CycleShuffle()

	case 'm':
		lib.GetMPV().CycleMute()

	case '<':
		lib.GetMPV().Prev()

	case '>':
		lib.GetMPV().Next()

	case ' ':
		lib.GetMPV().CyclePaused()

	default:
		norune = true
	}

	if !nokey || !norune {
		sendPlayerEvent()
	}
}

// sendPlayerEvent sends a player event.
func sendPlayerEvent() {
	select {
	case playerEvent <- struct{}{}:
		return

	default:
	}
}
