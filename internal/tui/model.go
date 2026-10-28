// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/secret"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// Screen identifies the visible view.
type Screen int

// Screens.
const (
	ScreenStart Screen = iota
	ScreenLoading
	ScreenSeries
	ScreenTatort
	ScreenQueue
	ScreenSummary
	ScreenLibrary
	ScreenDetails
	ScreenSeasonEpisodes
	ScreenSplash
	ScreenEditEntry
	ScreenCatalog
)

// Messages exchanged with the app through the program loop.

// formPurpose records what the start screen was opened for.
type formPurpose int

const (
	formStart formPurpose = iota
	formSettings
	formAddURL
)

// SeasonsRequest asks the user to pick seasons.
type SeasonsRequest struct {
	Show    string
	Seasons []app.Season
	Reply   chan []int
	Done    chan struct{}
}

// CategoriesRequest asks which Tatort categories to process.
type CategoriesRequest struct {
	Title      string
	Categories []app.Category
	Reply      chan []string
	Done       chan struct{}
}

// SkipRequest asks how many Tatort episodes to skip.
type SkipRequest struct {
	City  string
	Count int
	Reply chan int
	Done  chan struct{}
}

// EventMsg forwards one app event.
type EventMsg struct{ Event app.Event }

// toastLevel styles the short message above the key hints.
type toastLevel int

// Toast levels.
const (
	toastInfo toastLevel = iota
	toastSuccess
	toastError
)

// RunDoneMsg reports the end of the download run.
type RunDoneMsg struct{ Err error }

type libraryMsg struct {
	entries []store.Series
	err     error
}

type checkDoneMsg struct {
	results []app.CheckResult
	err     error
}

type downloadNewDoneMsg struct {
	entry store.Series
	err   error
}
type detailsRefreshDoneMsg struct {
	entryID int64
	err     error
}

type job struct {
	label  string
	path   string
	done   int
	total  int
	status string
	// The moments of the download, used for the remaining time.
	startedAt time.Time
	updatedAt time.Time
	lastDone  int
	// perSegment is the measured time per segment of the last update.
	perSegment time.Duration
	// The episode and the series the file belongs to.
	episodeID string
	seriesID  int64
}

type detailSeasonSummary struct {
	number int
	total  int
	open   int
}

// Options wires the model to the application.
type Options struct {
	Context          context.Context
	Config           *config.Options
	StartScreen      bool
	AutoStart        bool
	NeedsLogin       bool
	Store            *store.Store
	Secrets          secret.Store
	Version          string
	Splash           bool
	Run              func(context.Context) error
	Check            func(context.Context) ([]app.CheckResult, error)
	DownloadNew      func(context.Context, store.Series) error
	DownloadEpisodes func(context.Context, store.Series, []string) error
	RefreshEntry     func(context.Context, store.Series) error
	// Catalog loads every series and every playable movie of ARD Plus.
	Catalog func(context.Context) ([]ardplus.CatalogItem, error)
}

// Model is the TUI state.
type Model struct {
	opts       Options
	cfg        *config.Options
	rootCtx    context.Context
	ctx        context.Context
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
	busy       bool
	needsLogin bool
	store      *store.Store
	secrets    secret.Store

	screen       Screen
	splashTarget Screen
	version      string
	width        int
	height       int
	focus        int
	fields       []textinput.Model
	helpVisible  bool

	seasons         []app.Season
	selection       map[int]bool
	cursor          int
	seriesOffset    int
	showName        string
	categoryMode    bool
	categories      []app.Category
	categoriesReply chan []string
	skipField       textinput.Model
	tatortCity      string
	tatortCount     int
	skipErr         string
	// selectionErr reports an empty selection on the season and category
	// screen.
	selectionErr string

	library    []store.Series
	libCursor  int
	libOffset  int
	libMessage string
	checking   bool
	// confirm is the open confirmation window; nil when none is open.
	confirm *confirmPrompt
	// pendingRemovalFile waits for the second x that deletes a file.
	pendingRemovalFile bool
	// libSelected holds the marked entries of the library.
	libSelected map[int64]bool
	// The search field filters the library by title.
	search    textinput.Model
	searching bool

	// The settings of a single series.
	editEntry store.Series
	editSkip  textinput.Model
	editErr   string

	details              store.Series
	episodes             []store.KnownEpisode
	detailSeasons        []detailSeasonSummary
	detailSeasonCursor   int
	activeDetailSeason   int
	detailSeasonEpisodes []store.KnownEpisode
	detailCursor         int
	detailOffset         int
	detailChoice         map[int]bool
	detailChoiceExplicit bool
	detailMessage        string

	downloadReturnScreen Screen
	downloadReturnSeason int
	detailReturnSeason   int
	// downloadSeriesID is the entry of the running download.
	downloadSeriesID int64

	jobs []job
	log  []string
	// The catalog of ARD Plus with its own cursor, search and genre filter.
	catalog          []ardplus.CatalogItem
	catalogGenres    []string
	catalogGenre     string
	catalogTab       catalogTab
	catalogLoading   bool
	catalogErr       string
	catalogCursor    int
	catalogOffset    int
	catalogSearch    textinput.Model
	catalogSearching bool
	// detailsFrom is the screen the details return to.
	detailsFrom Screen

	// history is the protocol of the session, shown below the lists.
	history      []historyEntry
	logVisible   bool
	toast        string
	toastLevel   toastLevel
	spinner      spinner.Model
	clockStarted bool
	startErr     error
	doneErr      error
	exitCode     int
	cancelled    bool
	running      bool

	formPurpose     formPurpose
	pendingDownload *store.Series

	seasonsReply   chan []int
	skipReply      chan int
	seasonsDone    chan struct{}
	categoriesDone chan struct{}
	skipDone       chan struct{}
}

var (
	titleStyle    = theme.Title
	selectedStyle = theme.Selected
)

// styleInput applies the design-system styles to one text input.
func styleInput(input *textinput.Model) {
	input.PromptStyle = theme.InputPrompt
	input.TextStyle = theme.InputText
	input.PlaceholderStyle = theme.InputPlaceholder
	input.Cursor.Style = theme.InputCursor
}

// errQuit tells the app that the TUI closed while it waited for input.
var errQuit = errors.New("quit")

func newModel(opts Options) *Model {
	rootCtx := opts.Context
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(rootCtx)
	cfg := opts.Config
	model := &Model{
		opts:         opts,
		cfg:          cfg,
		rootCtx:      rootCtx,
		ctx:          ctx,
		cancel:       cancel,
		wg:           &sync.WaitGroup{},
		needsLogin:   opts.NeedsLogin,
		store:        opts.Store,
		secrets:      opts.Secrets,
		selection:    map[int]bool{},
		detailChoice: map[int]bool{},
		screen:       ScreenLoading,
	}
	model.loadLibrary()
	switch {
	case opts.StartScreen:
		model.screen = ScreenStart
	case opts.AutoStart:
		model.screen = ScreenLoading
	case opts.Catalog != nil:
		// The catalog is the home screen: every title of ARD Plus is there.
		model.screen = ScreenCatalog
	case len(model.library) > 0:
		// The library is the home screen; the form is one keystroke away.
		model.screen = ScreenLibrary
	default:
		// Without a configured URL there is nothing to run yet. The prefilled
		// last URL only populates the form.
		model.screen = ScreenStart
	}
	model.version = opts.Version
	if opts.Splash {
		// The splash screen hands over to the screen the app starts with.
		model.splashTarget = model.screen
		model.screen = ScreenSplash
		model.busy = true
	}
	model.loadTheme()

	url := textinput.New()
	url.Prompt = ""
	url.Placeholder = "https://www.ardplus.de/details/..."
	url.SetValue(cfg.URL)
	url.Width = 70
	styleInput(&url)

	user := textinput.New()
	user.Prompt = ""
	user.Placeholder = "ARD Plus username"
	user.SetValue(cfg.Username)
	user.Width = 40
	styleInput(&user)

	password := textinput.New()
	password.Prompt = ""
	password.Placeholder = "ARD Plus password"
	password.EchoMode = textinput.EchoPassword
	password.EchoCharacter = '•'
	password.Width = 40
	styleInput(&password)

	model.fields = []textinput.Model{url, user, password}
	model.logVisible = true
	model.spinner = spinner.New(spinner.WithSpinner(spinner.Dot))

	editSkip := textinput.New()
	editSkip.Prompt = ""
	editSkip.Width = 8
	editSkip.Placeholder = "0"
	styleInput(&editSkip)
	model.editSkip = editSkip

	search := textinput.New()
	search.Prompt = ""
	search.Width = 30
	search.Placeholder = "Titel suchen"
	styleInput(&search)
	model.search = search
	catalogSearch := textinput.New()
	catalogSearch.Prompt = ""
	catalogSearch.Width = 30
	catalogSearch.Placeholder = "Titel oder Genre suchen"
	styleInput(&catalogSearch)
	model.catalogSearch = catalogSearch
	model.skipField = textinput.New()
	model.skipField.Prompt = ""
	model.skipField.SetValue("0")
	model.skipField.Width = 8
	styleInput(&model.skipField)
	model.focusField(0)
	if model.hidesURLField() {
		model.focusField(1)
	}
	return model
}

func (m *Model) loadLibrary() {
	if m.store == nil {
		return
	}
	entries, err := m.store.SeriesList()
	if err != nil {
		m.setToast("Bibliothek: "+err.Error(), toastError)
		return
	}
	m.library = entries
	m.clampLibOffset()
}

func (m *Model) focusField(index int) {
	for i := range m.fields {
		if i == index {
			m.fields[i].Focus()
		} else {
			m.fields[i].Blur()
		}
	}
	m.focus = index
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = typed.Width, typed.Height
		m.clampLibOffset()
		m.clampSeriesOffset()
		m.clampDetailOffset()
		m.clampSeasonOffset()
		m.resizeFields()
		if !m.clockStarted {
			// The clock animates the loading indicator.
			m.clockStarted = true
			return m, m.clockTick()
		}
		return m, nil

	case splashDoneMsg:
		m.screen = m.splashTarget
		m.busy = false
		switch m.screen {
		case ScreenStart:
			return m, textinput.Blink
		case ScreenLibrary, ScreenCatalog:
			return m, nil
		default:
			return m, m.startRun()
		}

	case clockMsg:
		if m.busy {
			m.spinner, _ = m.spinner.Update(spinner.TickMsg{})
		}
		return m, m.clockTick()

	case SeasonsRequest:
		m.categoryMode = false
		m.categoriesReply = nil
		m.showName = typed.Show
		m.seasons = typed.Seasons
		m.cursor = 0
		m.seriesOffset = 0
		m.selection = map[int]bool{}
		for _, season := range typed.Seasons {
			m.selection[season.Number] = true
		}
		m.seasonsReply = typed.Reply
		m.seasonsDone = typed.Done
		m.screen = ScreenSeries
		return m, nil

	case CategoriesRequest:
		m.showName = typed.Title
		m.categories = typed.Categories
		m.seasons = make([]app.Season, 0, len(typed.Categories))
		for i, category := range typed.Categories {
			m.seasons = append(m.seasons, app.Season{Number: i + 1, Title: category.Title, ID: category.Slug})
		}
		m.selection = map[int]bool{} // nothing preselected: the archive is large
		m.cursor = 0
		m.seriesOffset = 0
		m.categoryMode = true
		m.categoriesReply = typed.Reply
		m.categoriesDone = typed.Done
		m.screen = ScreenSeries
		return m, nil

	case SkipRequest:
		m.tatortCity = typed.City
		m.tatortCount = typed.Count
		m.skipErr = ""
		m.skipField.SetValue("0")
		m.skipField.Focus()
		m.skipReply = typed.Reply
		m.skipDone = typed.Done
		m.screen = ScreenTatort
		return m, textinput.Blink

	case EventMsg:
		m.applyEvent(typed.Event)
		return m, nil

	case libraryMsg:
		return m.handleLibraryMsg(typed)

	case pendingCountMsg:
		return m.handlePendingCountMsg(typed)

	case storeActionMsg:
		return m.handleStoreActionMsg(typed)

	case detailsLoadedMsg:
		return m.handleDetailsLoadedMsg(typed)

	case catalogMsg:
		return m.handleCatalogMsg(typed)

	case checkDoneMsg:
		m.setBusy(false)
		m.checking = false
		if typed.err != nil {
			m.exitCode = 1
			m.setToast("Prüfung fehlgeschlagen: "+typed.err.Error(), toastError)
			return m, m.loadLibraryCmd()
		}
		newEpisodes := 0
		failed := 0
		firstFailure := ""
		notes := 0
		firstNote := ""
		for _, result := range typed.results {
			newEpisodes += len(result.New)
			if result.Err != nil {
				failed++
				if firstFailure == "" {
					firstFailure = result.Err.Error()
				}
			}
			if result.Note != "" {
				notes++
				if firstNote == "" {
					firstNote = result.Note
				}
			}
		}
		summary := fmt.Sprintf("%d Serie(n) geprüft, %d neue Folge(n).", len(typed.results), newEpisodes)
		level := toastSuccess
		if failed > 0 {
			m.exitCode = 1
			level = toastError
			summary += fmt.Sprintf(" %d fehlgeschlagen: %s", failed, firstFailure)
		}
		if notes > 0 {
			label := "Hinweise"
			if notes == 1 {
				label = "Hinweis"
			}
			summary += fmt.Sprintf(" %d %s: %s", notes, label, firstNote)
		}
		m.setToast(summary, level)
		return m, m.loadLibraryCmd()

	case downloadNewDoneMsg:
		m.setBusy(false)
		if typed.err != nil {
			m.exitCode = 1
		}
		returnScreen := m.downloadReturnScreen
		returnSeason := m.downloadReturnSeason
		m.downloadReturnScreen = ScreenStart
		m.downloadReturnSeason = 0
		m.cancelled = false
		switch returnScreen {
		case ScreenLibrary:
			m.screen = ScreenLibrary
		case ScreenStart:
			// Downloads from the URL flow stay on the queue/summary screens.
		default:
			cmd := m.openDetails(m.details)
			if returnSeason > 0 {
				m.detailReturnSeason = returnSeason
			}
			if typed.err != nil {
				m.detailMessage = typed.err.Error()
			} else {
				m.detailMessage = "Fertig."
			}
			return m, tea.Batch(cmd, m.loadLibraryCmd())
		}
		if typed.err != nil {
			m.setToast(entryTitle(typed.entry)+": "+typed.err.Error(), toastError)
		} else if returnScreen == ScreenLibrary {
			m.setToast(entryTitle(typed.entry)+": Folgen geladen.", toastSuccess)
		}
		return m, m.loadLibraryCmd()

	case detailsRefreshDoneMsg:
		m.setBusy(false)
		if typed.err != nil {
			m.exitCode = 1
		}
		if (m.screen == ScreenDetails || m.screen == ScreenSeasonEpisodes) && typed.entryID == m.details.ID {
			returnToSeason := m.screen == ScreenSeasonEpisodes
			seasonNumber := m.activeDetailSeason
			cmd := m.openDetails(m.details)
			if returnToSeason {
				m.detailReturnSeason = seasonNumber
			}
			if typed.err != nil {
				m.detailMessage = "Folgenliste konnte nicht aktualisiert werden: " + typed.err.Error()
			} else {
				m.detailMessage = ""
			}
			return m, tea.Batch(cmd, m.loadLibraryCmd())
		}
		return m, m.loadLibraryCmd()

	case RunDoneMsg:
		m.running = false
		m.setBusy(false)
		m.doneErr = typed.Err
		if typed.Err != nil {
			m.exitCode = 1
		}
		m.screen = ScreenSummary
		if m.cancelled {
			return m.quit(130)
		}
		return m, m.loadLibraryCmd()

	case tea.KeyMsg:
		return m.handleKey(typed)
	}
	return m, nil
}

// Init starts the run or the input screen.
func (m *Model) Init() tea.Cmd {
	// The catalog loads in the background from the first moment, so it is
	// ready when the splash screen or the form hands over.
	catalog := m.loadCatalogCmd()
	if m.screen == ScreenSplash {
		return tea.Batch(m.splashTick(), m.clockTick(), catalog)
	}
	if m.screen == ScreenStart {
		return tea.Batch(textinput.Blink, catalog)
	}
	if m.screen == ScreenLibrary || m.screen == ScreenCatalog {
		return catalog
	}
	return tea.Batch(m.startRun(), catalog)
}

func (m *Model) startRun() tea.Cmd {
	if m.running {
		return nil
	}
	m.running = true
	m.jobs = nil
	m.log = nil
	m.cancelled = false
	m.screen = ScreenLoading
	run := m.opts.Run
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return RunDoneMsg{Err: run(ctx)}
	})
}

// track keeps the wait group balanced so Close can wait for running commands.
func (m *Model) track(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	wg := m.wg
	wg.Add(1)
	return func() tea.Msg {
		defer wg.Done()
		return cmd()
	}
}

// release cancels the run and wakes up pending UI requests.
func (m *Model) release() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.seasonsDone != nil {
		close(m.seasonsDone)
		m.seasonsDone = nil
	}
	if m.categoriesDone != nil {
		close(m.categoriesDone)
		m.categoriesDone = nil
	}
	if m.skipDone != nil {
		close(m.skipDone)
		m.skipDone = nil
	}
}

// Close cancels the run and waits up to five seconds for pending commands.
func (m *Model) Close() {
	m.release()
	if m.wg != nil {
		done := make(chan struct{})
		go func() {
			m.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// ExitCode is the process exit code for the finished run.
func (m *Model) ExitCode() int { return m.exitCode }

// quit releases pending UI requests and ends the program.
func (m *Model) quit(code int) (tea.Model, tea.Cmd) {
	if code != 0 {
		m.exitCode = code
	}
	m.release()
	m.seasonsReply = nil
	m.categoriesReply = nil
	m.skipReply = nil
	return m, tea.Quit
}

func (m *Model) checkLibrary() tea.Cmd {
	if m.opts.Check == nil {
		return nil
	}
	if m.busy {
		return nil
	}
	m.checking = true
	m.libMessage = "Prüfe gespeicherte Serien…"
	check := m.opts.Check
	ctx := m.ctx
	m.setBusy(true)
	return m.track(func() tea.Msg {
		results, err := check(ctx)
		return checkDoneMsg{results: results, err: err}
	})
}

func (m *Model) downloadNew(entry store.Series) tea.Cmd {
	if m.opts.DownloadNew == nil {
		return nil
	}
	if m.busy {
		return nil
	}
	m.libMessage = "Lade neue Folgen von " + entryTitle(entry) + "…"
	m.downloadSeriesID = entry.ID
	m.beginDownload()
	downloadNew := m.opts.DownloadNew
	ctx := m.ctx
	m.setBusy(true)
	return m.track(func() tea.Msg {
		return downloadNewDoneMsg{entry: entry, err: downloadNew(ctx, entry)}
	})
}

// beginDownload shows the progress pane and remembers the screen to return to.
func (m *Model) beginDownload() {
	m.cancelled = false
	m.jobs = nil
	m.log = nil
	if m.ctx.Err() != nil {
		m.ctx, m.cancel = context.WithCancel(m.rootCtx)
	}
	m.downloadReturnScreen = m.screen
	m.downloadReturnSeason = 0
	if m.screen == ScreenSeasonEpisodes {
		m.downloadReturnSeason = m.activeDetailSeason
	}
	m.screen = ScreenQueue
}

// Update handles one message.
