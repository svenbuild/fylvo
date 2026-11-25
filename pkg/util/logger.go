package util

import (
	"fmt"
	"github.com/fatih/color"
	"sync"
	"time"
)

const (
	// LevelError error
	LevelError = iota
	// LevelWarning warning
	LevelWarning
	// LevelInformational info
	LevelInformational
	// LevelDebug debug
	LevelDebug
)

var GloablLogger *Logger
var Level = LevelDebug

// Logger log
type Logger struct {
	level int
	mu    sync.Mutex
}

// log colors
var colors = map[string]func(a ...interface{}) string{
	"Warning": color.New(color.FgYellow).Add(color.Bold).SprintFunc(),
	"Panic":   color.New(color.BgRed).Add(color.Bold).SprintFunc(),
	"Error":   color.New(color.FgRed).Add(color.Bold).SprintFunc(),
	"Info":    color.New(color.FgCyan).Add(color.Bold).SprintFunc(),
	"Debug":   color.New(color.FgWhite).Add(color.Bold).SprintFunc(),
}

// spacing between level prefix and time, keeps the width consistent
var spaces = map[string]string{
	"Warning": "",
	"Panic":   "  ",
	"Error":   "  ",
	"Info":    "   ",
	"Debug":   "  ",
}

// Println print
func (ll *Logger) Println(prefix string, msg string) {
	// TODO Release: remove
	// color.NoColor = false

	c := color.New()

	ll.mu.Lock()
	defer ll.mu.Unlock()

	_, _ = c.Printf(
		"%s%s %s %s\n",
		colors[prefix]("["+prefix+"]"),
		spaces[prefix],
		time.Now().Format("2006-01-02 15:04:05"),
		msg,
	)
}

// Panic fatal error
func (ll *Logger) Panic(format string, v ...interface{}) {
	if LevelError > ll.level {
		return
	}
	msg := fmt.Sprintf(format, v...)
	ll.Println("Panic", msg)
	panic(msg)
}

// Error error
func (ll *Logger) Error(format string, v ...interface{}) {
	if LevelError > ll.level {
		return
	}
	msg := fmt.Sprintf(format, v...)
	ll.Println("Error", msg)
}

// Warning warning
func (ll *Logger) Warning(format string, v ...interface{}) {
	if LevelWarning > ll.level {
		return
	}
	msg := fmt.Sprintf(format, v...)
	ll.Println("Warning", msg)
}

// Info info
func (ll *Logger) Info(format string, v ...interface{}) {
	if LevelInformational > ll.level {
		return
	}
	msg := fmt.Sprintf(format, v...)
	ll.Println("Info", msg)
}

// Debug debug
func (ll *Logger) Debug(format string, v ...interface{}) {
	if LevelDebug > ll.level {
		return
	}
	msg := fmt.Sprintf(format, v...)
	ll.Println("Debug", msg)
}

// Print GORM's Logger implementation
//func (ll *Logger) Print(v ...interface{}) {
//	if LevelDebug > ll.level {
//		return
//	}
//	msg := fmt.Sprintf("[SQL] %s", v...)
//	ll.println(msg)
//}

// Log returns the logger
func Log() *Logger {
	if GloablLogger == nil {
		l := Logger{
			level: Level,
		}
		GloablLogger = &l
	}
	return GloablLogger
}
