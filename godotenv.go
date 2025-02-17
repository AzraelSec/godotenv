// Package godotenv is a go port of the ruby dotenv library (https://github.com/bkeepers/dotenv)
//
// Examples/readme can be found on the GitHub page at https://github.com/joho/godotenv
//
// The TL;DR is that you make a .env file that looks something like
//
//	SOME_ENV_VAR=somevalue
//
// and then in your go code you can call
//
//	godotenv.Load()
//
// and all the env vars declared in .env will be available through os.Getenv("SOME_ENV_VAR")
package godotenv

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
)

const doubleQuoteSpecialChars = "\\\n\r\"!$`"

var noEnvFileLoadedErr = errors.New("no env file loaded")

// Parse reads an env file from io.Reader, returning a map of keys and values.
func Parse(r io.Reader) (map[string]string, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	if err != nil {
		return nil, err
	}

	return UnmarshalBytes(buf.Bytes())
}

// Load will read your env file(s) and load them into ENV for this process.
//
// Call this function as close as possible to the start of your program (ideally in main).
//
// If you call Load without any args it will default to loading .env in the current path.
//
// You can otherwise tell it which files to load (there can be more than one) like:
//
//	godotenv.Load("fileone", "filetwo")
//
// It's important to note that it WILL NOT OVERRIDE an env variable that already exists - consider the .env file to set dev vars or sensible defaults.
func Load(strict bool, filenames ...string) (err error) {
	return LoadFrom("./", strict, filenames...)
}

func LoadFrom(dir string, strict bool, filenames ...string) (err error) {
	filenames = filenamesOrDefault(filenames)
	loaded := false

	for _, filename := range filenames {
		innerErr := loadFile(dir, filename, false)
		if innerErr != nil && strict {
			err = innerErr
			return // return early on a spazout
		}
		if innerErr == nil {
			loaded = true
		}
	}

	if !loaded {
		err = noEnvFileLoadedErr
	}
	return
}

// Overload will read your env file(s) and load them into ENV for this process.
//
// Call this function as close as possible to the start of your program (ideally in main).
//
// If you call Overload without any args it will default to loading .env in the current path.
//
// You can otherwise tell it which files to load (there can be more than one) like:
//
//	godotenv.Overload("fileone", "filetwo")
//
// It's important to note this WILL OVERRIDE an env variable that already exists - consider the .env file to forcefully set all vars.
func Overload(strict bool, filenames ...string) (err error) {
	return OverloadFrom("./", strict, filenames...)
}

func OverloadFrom(dir string, strict bool, filenames ...string) (err error) {
	filenames = filenamesOrDefault(filenames)
	loaded := false

	for _, filename := range filenames {
		innerErr := loadFile(dir, filename, true)
		if innerErr != nil && strict {
			err = innerErr
			return // return early on a spazout
		}
		if innerErr != nil && !strict {
			continue
		}
		loaded = true
	}

	if !loaded {
		err = noEnvFileLoadedErr
	}
	return
}

// Read all env (with same file loading semantics as Load) but return values as
// a map rather than automatically writing values into env
func Read(strict bool, filenames ...string) (envMap map[string]string, err error) {
	return ReadFrom("./", strict, filenames...)
}

func ReadFrom(dir string, strict bool, filenames ...string) (envMap map[string]string, err error) {
	filenames = filenamesOrDefault(filenames)
	envMap = make(map[string]string)
	loaded := false

	for _, filename := range filenames {
		individualEnvMap, individualErr := readFile(dir, filename)

		if individualErr != nil && strict {
			err = individualErr
			return // return early on a spazout
		}
		if individualErr != nil && !strict {
			continue
		}

		loaded = true
		for key, value := range individualEnvMap {
			envMap[key] = value
		}
	}

	if !loaded {
		err = noEnvFileLoadedErr
	}
	return
}

// Unmarshal reads an env file from a string, returning a map of keys and values.
func Unmarshal(str string) (envMap map[string]string, err error) {
	return UnmarshalBytes([]byte(str))
}

// UnmarshalBytes parses env file from byte slice of chars, returning a map of keys and values.
func UnmarshalBytes(src []byte) (map[string]string, error) {
	out := make(map[string]string)
	err := parseBytes(src, out)

	return out, err
}

// Exec loads env vars from the specified filenames (empty map falls back to default)
// then executes the cmd specified.
//
// Simply hooks up os.Stdin/err/out to the command and calls Run().
//
// If you want more fine grained control over your command it's recommended
// that you use `Load()`, `Overload()` or `Read()` and the `os/exec` package yourself.
func Exec(filenames []string, cmd string, cmdArgs []string, strict, overload bool) error {
	op := Load
	if overload {
		op = Overload
	}
	if err := op(strict, filenames...); err != nil {
		return err
	}

	command := exec.Command(cmd, cmdArgs...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

// Write serializes the given environment and writes it to a file.
func Write(envMap map[string]string, filename string) error {
	content, err := Marshal(envMap)
	if err != nil {
		return err
	}
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content + "\n")
	if err != nil {
		return err
	}
	return file.Sync()
}

// Marshal outputs the given environment as a dotenv-formatted environment file.
// Each line is in the format: KEY="VALUE" where VALUE is backslash-escaped.
func Marshal(envMap map[string]string) (string, error) {
	lines := make([]string, 0, len(envMap))
	for k, v := range envMap {
		if d, err := strconv.Atoi(v); err == nil {
			lines = append(lines, fmt.Sprintf(`%s=%d`, k, d))
		} else {
			lines = append(lines, fmt.Sprintf(`%s="%s"`, k, doubleQuoteEscape(v)))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

func filenamesOrDefault(filenames []string) []string {
	if len(filenames) == 0 {
		return []string{".env"}
	}
	return filenames
}

func loadFile(dir, filename string, overload bool) error {
	envMap, err := readFile(dir, filename)
	if err != nil {
		return err
	}

	currentEnv := map[string]bool{}
	rawEnv := os.Environ()
	for _, rawEnvLine := range rawEnv {
		key := strings.Split(rawEnvLine, "=")[0]
		currentEnv[key] = true
	}

	for key, value := range envMap {
		if !currentEnv[key] || overload {
			_ = os.Setenv(key, value)
		}
	}

	return nil
}

func readFile(dir, filename string) (envMap map[string]string, err error) {
	file, err := os.Open(path.Join(dir, filename))
	if err != nil {
		return
	}
	defer file.Close()

	return Parse(file)
}

func doubleQuoteEscape(line string) string {
	for _, c := range doubleQuoteSpecialChars {
		toReplace := "\\" + string(c)
		if c == '\n' {
			toReplace = `\n`
		}
		if c == '\r' {
			toReplace = `\r`
		}
		line = strings.Replace(line, string(c), toReplace, -1)
	}
	return line
}
