package core

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

//implements io.Writer interface

type Logger interface {
	io.WriteCloser
	ReadLog(offset int64, length int64) (string, error)
	ReadTailLog(offset int64, length int64) (string, int64, bool, error)
	ClearCurLogFile() error
	ClearAllLogFile() error
}

type FileLogger struct {
	name      string
	maxSize   int64
	backups   int
	curRotate int
	fileSize  int64
	file      *os.File
	locker    sync.Locker
}

type NullLogger struct {
}

type NullLocker struct {
}

func NewFileLogger(name string, maxSize int64, backups int, locker sync.Locker) *FileLogger {
	logger := &FileLogger{name: name,
		maxSize:   maxSize,
		backups:   backups,
		curRotate: -1,
		fileSize:  0,
		file:      nil,
		locker:    locker}
	logger.updateLatestLog()
	return logger
}

// return the next log file name
func (l *FileLogger) nextLogFile() {
	l.curRotate++
	if l.curRotate >= l.backups {
		l.curRotate = 0
	}
}

func (l *FileLogger) updateLatestLog() {
	dir := path.Dir(l.name)
	files, err := ioutil.ReadDir(dir)

	if err != nil {
		l.curRotate = 0
	} else {
		//find all the rotate files
		var latestFile os.FileInfo
		latestNum := -1
		for _, fileInfo := range files {
			if strings.HasPrefix(fileInfo.Name(), l.name+".") {
				n, err := strconv.Atoi(fileInfo.Name()[len(l.name)+1:])
				if err == nil && n >= 0 && n < l.backups {
					if latestFile == nil || latestFile.ModTime().Before(fileInfo.ModTime()) {
						latestFile = fileInfo
						latestNum = n
					}
				}
			}
		}
		l.curRotate = latestNum
		if latestFile != nil {
			l.fileSize = latestFile.Size()
		} else {
			l.fileSize = int64(0)
		}
		if l.fileSize >= l.maxSize || latestFile == nil {
			l.nextLogFile()
			l.openFile(true)
		} else {
			l.openFile(false)
		}
	}
}

// open the file and truncate the file if trunc is true
func (l *FileLogger) openFile(trunc bool) error {
	if l.file != nil {
		l.file.Close()
	}
	var err error
	fileName := l.GetCurrentLogFile()
	if trunc {
		l.file, err = os.Create(fileName)
	} else {
		l.file, err = os.OpenFile(fileName, os.O_RDWR|os.O_APPEND, 0666)
	}
	return err
}

// get the name of current log file
func (l *FileLogger) GetCurrentLogFile() string {
	return l.getLogFileName(l.curRotate)
}

// get the name of previous log file
func (l *FileLogger) GetPrevLogFile() string {
	i := (l.curRotate - 1 + l.backups) % l.backups

	return l.getLogFileName(i)
}

func (l *FileLogger) getLogFileName(index int) string {
	return fmt.Sprintf("%s.%d", l.name, index)
}

// clear the current log file contents
func (l *FileLogger) ClearCurLogFile() error {
	l.locker.Lock()
	defer l.locker.Unlock()

	return l.openFile(true)
}

func (l *FileLogger) ClearAllLogFile() error {
	l.locker.Lock()
	defer l.locker.Unlock()

	for i := 0; i < l.backups; i++ {
		logFile := l.getLogFileName(i)
		err := os.Remove(logFile)
		if err != nil {
			return NewFault(FAILED, "FAILED")
		}
	}
	l.curRotate = 0
	err := l.openFile(true)
	if err != nil {
		return NewFault(FAILED, "FAILED")
	}
	return nil
}

func (l *FileLogger) ReadLog(offset int64, length int64) (string, error) {
	if offset < 0 && length != 0 {
		return "", NewFault(BAD_ARGUMENTS, "BAD_ARGUMENTS")
	}
	if offset >= 0 && length < 0 {
		return "", NewFault(BAD_ARGUMENTS, "BAD_ARGUMENTS")
	}

	l.locker.Lock()
	defer l.locker.Unlock()
	f, err := os.Open(l.GetCurrentLogFile())

	if err != nil {
		return "", NewFault(FAILED, "FAILED")
	}
	defer f.Close()

	//check the length of file
	statInfo, err := f.Stat()
	if err != nil {
		return "", NewFault(FAILED, "FAILED")
	}

	fileLen := statInfo.Size()

	if offset < 0 { //offset < 0 && length == 0
		offset = fileLen + offset
		if offset < 0 {
			offset = 0
		}
		length = fileLen - offset
	} else if length == 0 { //offset >= 0 && length == 0
		if offset > fileLen {
			return "", nil
		}
		length = fileLen - offset
	} else { //offset >= 0 && length > 0

		//if the offset exceeds the length of file
		if offset >= fileLen {
			return "", nil
		}

		//compute actual bytes should be read

		if offset+length > fileLen {
			length = fileLen - offset
		}
	}

	b := make([]byte, length)
	n, err := f.ReadAt(b, offset)
	if err != nil {
		return "", NewFault(FAILED, "FAILED")
	}
	return string(b[:n]), nil
}

func (l *FileLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	if offset < 0 {
		return "", offset, false, fmt.Errorf("offset should not be less than 0")
	}
	if length < 0 {
		return "", offset, false, fmt.Errorf("length should be not be less than 0")
	}
	l.locker.Lock()
	defer l.locker.Unlock()

	//open the file
	f, err := os.Open(l.GetCurrentLogFile())
	if err != nil {
		return "", 0, false, err
	}

	defer f.Close()

	//get the length of file
	statInfo, err := f.Stat()
	if err != nil {
		return "", 0, false, err
	}

	fileLen := statInfo.Size()

	//check if offset exceeds the length of file
	if offset >= fileLen {
		return "", fileLen, true, nil
	}

	//get the length
	// 如果文件只有100字节，偏移量80，预读取50字节，那么最终只能读取20字节（只有这么多）
	if offset+length > fileLen {
		length = fileLen - offset
	}

	b := make([]byte, length)
	n, err := f.ReadAt(b, offset)
	if err != nil {
		return "", offset, false, err
	}
	return string(b[:n]), offset + int64(n), false, nil

}

// Override the function in io.Writer
func (l *FileLogger) Write(p []byte) (int, error) {
	l.locker.Lock()
	defer l.locker.Unlock()

	n, err := l.file.Write(p)

	if err != nil {
		return n, err
	}
	l.fileSize += int64(n)
	if l.fileSize >= l.maxSize {
		fileInfo, err := os.Stat(fmt.Sprintf("%s.%d", l.name, l.curRotate))
		if err == nil {
			l.fileSize = fileInfo.Size()
		} else {
			return n, err
		}
	}
	if l.fileSize >= l.maxSize {
		l.nextLogFile()
		l.openFile(true)
	}
	return n, err
}

func (l *FileLogger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func NewNullLogger() *NullLogger {
	return &NullLogger{}
}

func (l *NullLogger) Write(p []byte) (int, error) {
	return len(p), nil
}

func (l *NullLogger) Close() error {
	return nil
}

func (l *NullLogger) ReadLog(offset int64, length int64) (string, error) {
	return "", NewFault(NO_FILE, "NO_FILE")
}

func (l *NullLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	return "", 0, false, NewFault(NO_FILE, "NO_FILE")
}

func (l *NullLogger) ClearCurLogFile() error {
	return fmt.Errorf("No log")
}

func (l *NullLogger) ClearAllLogFile() error {
	return NewFault(NO_FILE, "NO_FILE")
}

func NewNullLocker() *NullLocker {
	return &NullLocker{}
}

func (l *NullLocker) Lock() {
}

func (l *NullLocker) Unlock() {
}

type StdoutLogger struct {
}

func NewStdoutLogger() *StdoutLogger {
	return &StdoutLogger{}
}

func (l *StdoutLogger) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (l *StdoutLogger) Close() error {
	return nil
}

func (l *StdoutLogger) ReadLog(offset int64, length int64) (string, error) {
	return "", NewFault(NO_FILE, "NO_FILE")
}

func (l *StdoutLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	return "", 0, false, NewFault(NO_FILE, "NO_FILE")
}

func (l *StdoutLogger) ClearCurLogFile() error {
	return fmt.Errorf("No log")
}

func (l *StdoutLogger) ClearAllLogFile() error {
	return NewFault(NO_FILE, "NO_FILE")
}

type StderrLogger struct {
}

func NewStderrLogger() *StderrLogger {
	return &StderrLogger{}
}

func (l *StderrLogger) Write(p []byte) (int, error) {
	return os.Stderr.Write(p)
}

func (l *StderrLogger) Close() error {
	return nil
}

func (l *StderrLogger) ReadLog(offset int64, length int64) (string, error) {
	return "", NewFault(NO_FILE, "NO_FILE")
}

func (l *StderrLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	return "", 0, false, NewFault(NO_FILE, "NO_FILE")
}

func (l *StderrLogger) ClearCurLogFile() error {
	return fmt.Errorf("No log")
}

func (l *StderrLogger) ClearAllLogFile() error {
	return NewFault(NO_FILE, "NO_FILE")
}
