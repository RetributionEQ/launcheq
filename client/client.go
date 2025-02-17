package client

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/md5"
	"embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/inconshreveable/mousetrap"
	"github.com/xackery/launcheq/config"
	"gopkg.in/yaml.v3"

	"github.com/fynelabs/selfupdate"
)

//go:embed rof2.torrent
var torrentContent embed.FS
var isMapsDownloaded bool

// Client wraps the entire UI
type Client struct {
	baseName      string
	patcherUrl    string
	currentPath   string
	clientVersion string
	isPatched     bool
	patchSummary  string
	cfg           *config.Config
	cacheFileList *FileList
	version       string
	cacheLog      string
	httpClient    *http.Client
}

// New creates a new client
func New(version string, patcherUrl string) (*Client, error) {
	var err error
	c := &Client{
		clientVersion: "rof",
		patcherUrl:    patcherUrl,
		version:       version,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	exeName, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("executable: %w", err)
	}

	c.baseName = filepath.Base(exeName)
	if strings.Contains(c.baseName, ".") {
		c.baseName = c.baseName[0:strings.Index(c.baseName, ".")]
	}

	c.cfg, err = config.New(context.Background(), c.baseName)
	if err != nil {
		return nil, fmt.Errorf("config.new: %w", err)
	}
	c.logf("Starting %s %s", c.baseName, c.version)
	c.currentPath, err = os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("wd invalid: %w", err)
	}

	return c, nil
}

func (c *Client) Sanitize() {

	endPath := c.currentPath
	// trim ending / or \\
	if strings.HasSuffix(endPath, "/") || strings.HasSuffix(endPath, "\\") {
		endPath = endPath[0 : len(endPath)-1]
	}

	// check if we're in the user's download path
	if strings.HasSuffix(endPath, "Downloads") {
		c.logf("You are running %s from your Downloads folder. This is not recommended.", c.baseName)
		c.logf("Please move %s to a different folder, such as C:\\Games\\EverQuest.", c.baseName)
		Exit(1)
	}

	// check if we're in the user's documents path
	if strings.HasSuffix(endPath, "Documents") {
		c.logf("You are running %s from your Documents folder. This is not recommended.", c.baseName)
		c.logf("Please move %s to a different folder, such as C:\\Games\\EverQuest.", c.baseName)
		Exit(1)
	}

	// check if we're in the user's desktop path
	if strings.HasSuffix(endPath, "Desktop") {
		c.logf("You are running %s from your Desktop folder. This is not recommended.", c.baseName)
		c.logf("Please move %s to a different folder, such as C:\\Games\\EverQuest.", c.baseName)
		Exit(1)
	}
}

func (c *Client) PrePatch() {

	_, err := os.Stat("eqgame.exe")
	if err != nil {
		rofPath := "everquest_rof2"
		_, err = os.Stat(rofPath + "/eqgame.exe")
		if err == nil {
			err = c.CopyBackup(rofPath)
			if err != nil {
				fmt.Printf("Failed to copy from everquest_rof2: %s\n", err)
				Exit(1)
			}
			return
		}
		rofPath = "../everquest_rof2"
		_, err = os.Stat(rofPath + "/eqgame.exe")
		if err == nil {
			err = c.CopyBackup(rofPath)
			if err != nil {
				fmt.Printf("Failed to copy from ../everquest_rof2: %s\n", err)
				Exit(1)
			}
			return
		}

		err = c.Torrent()
		if err != nil {
			fmt.Printf("Failed to download: %s\n", err)
			Exit(1)
		}
		err = c.CopyBackup("everquest_rof2")
		if err != nil {
			fmt.Printf("Failed to copy from everquest_rof2: %s\n", err)
			if runtime.GOOS == "windows" {
				c.logf("Close this window or press CTRL+C to exit.")
				Exit(1)
			}
		}
	}
}

// Patch starts the patching process
func (c *Client) Patch() {
	start := time.Now()
	isErrored := false

	err := c.selfUpdateAndPatch()
	if err != nil {
		c.logf("Failed patch: %s", err)
		isErrored = true
	}

	username, err := c.fetchUsername()
	if err != nil {
		c.logf("Failed grabbing username from eqlsPlayerData.ini: %s", err)
		//this error is not critical
	}
	if username == "" {
		username = "x"
	}

	if c.isPatched {
		c.logf(c.patchSummary)
		c.logf("You can check %s.txt if you wish to review the patched files later.", c.baseName)
		if isErrored {
			c.logf("Since patching failed, keeping window open.")
			Exit(1)
		}
		c.logf("Since files were patched, waiting 5 seconds before launching EverQuest...")
		time.Sleep(5 * time.Second)
	}

	if !c.cfg.IsAutoLaunch {
		c.logf("Finished in %0.2f seconds", time.Since(start).Seconds())
		c.logf("EverQuest is now patched and up to date.")
		Exit(0)
		return
	}

	c.logf("Launching EverQuest from %s", c.currentPath)
	cmd := c.createCommand(true, fmt.Sprintf("%s/eqgame.exe", c.currentPath), "patchme", "/login:"+username)
	cmd.Dir = c.currentPath
	err = cmd.Start()
	if err != nil {
		c.logf("Failed to run eqgame.exe: %s", err)
		isErrored = true
	}

	c.logf("Finished in %0.2f seconds", time.Since(start).Seconds())

	err = os.WriteFile(fmt.Sprintf("%s.txt", c.baseName), []byte(c.cacheLog), os.ModePerm)
	if err != nil {
		fmt.Println("Failed to write log:", err)
		isErrored = true
	}

	if isErrored && runtime.GOOS == "windows" {
		fmt.Printf("There was an error while launching EQ. Review above or %s.txt to see why.\n", c.baseName)
		fmt.Println("Automatically exiting in 10 seconds...")
		time.Sleep(10 * time.Second)
	}
}

func (c *Client) selfUpdateAndPatch() error {
	var err error

	err = c.fetchFileList()
	if err != nil {
		c.logf("Failed fetch file list, skipping: %s", err)
		return nil
	}

	err = c.patch()
	if err != nil {
		return fmt.Errorf("patch: %w", err)
	}

	err = c.selfUpdate()
	if err != nil {
		if strings.Contains(err.Error(), "because the file contains a virus or potentially unwanted software") {
			fmt.Println("Your antivirus is blocking", c.baseName, "from being patched. You need to go into your antivirus and recover the file from quarentine.")
			Exit(1)
		}
		c.logf("Failed self update, skipping: %s", err)
	}

	return nil
}

func (c *Client) fetchFileList() error {
	client := c.httpClient
	url := fmt.Sprintf("%s/filelist_%s.yml", c.patcherUrl, c.clientVersion)
	c.logf("Downloading %s", url)
	resp, err := client.Get(url)
	if err != nil {
		url := fmt.Sprintf("%s/%s/filelist_%s.yml", c.patcherUrl, c.clientVersion, c.clientVersion)
		c.logf("Downloading legacy %s", url)
		resp, err = client.Get(url)
		if err != nil {
			return fmt.Errorf("download %s: %w", url, err)
		}
	}
	if resp.StatusCode != 200 {
		c.cacheFileList = &FileList{}
		return fmt.Errorf("download %s responded %d (not 200)", url, resp.StatusCode)
	}

	defer resp.Body.Close()
	fileList := &FileList{}

	err = yaml.NewDecoder(resp.Body).Decode(fileList)
	if err != nil {
		return fmt.Errorf("decode filelist: %w", err)
	}
	//c.logf("patch version is", fileList.Version, "and we are version", c.cfg.ClientVersion)
	c.cacheFileList = fileList
	return nil
}

func (c *Client) selfUpdate() error {
	client := c.httpClient

	exeName, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}

	baseName := c.baseName

	err = os.Remove(baseName + ".bat")
	if err != nil {
		if !os.IsNotExist(err) {
			c.logf("Failed to remove %s.bat: %s", baseName, err)
		}
	} else {
		c.logf("Removed %s.bat", baseName)
	}

	err = os.Remove("." + baseName + ".exe.old")
	if err != nil {
		if !os.IsNotExist(err) {
			c.logf("Failed to remove .%s.exe.old: %s", baseName, err)
		}
	} else {
		c.logf("Removed .%s.exe.old", baseName)
	}

	myHash, err := md5Checksum(exeName)
	if err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	url := fmt.Sprintf("%s/launcheq-hash.txt", c.patcherUrl)
	c.logf("Checking for self update at %s", url)
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s responded %d (not 200)", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", url, err)
	}

	myHash = strings.ToUpper(strings.TrimSpace(myHash))
	remoteHash := strings.ToUpper(strings.TrimSpace(string(data)))

	if remoteHash == "Not Found" {
		c.logf("Remote site down, ignoring self update")
		return nil
	}

	if myHash == remoteHash {
		c.logf("Self update not needed")
		return nil
	}

	c.logf("Updating %s... %s vs %s", c.baseName, myHash, remoteHash)

	url = fmt.Sprintf("%s/%s.exe", c.patcherUrl, c.baseName)
	c.logf("Downloading %s at %s", c.baseName, url)
	resp, err = client.Get(url)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s responded %d (not 200)", url, resp.StatusCode)
	}
	c.logf("Applying update (will be used next launch)")
	err = selfupdate.Apply(resp.Body, selfupdate.Options{})
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	//isErrored := false

	// c.logf("Creating %s.bat", c.baseName)
	// err = os.WriteFile(fmt.Sprintf("%s.bat", c.baseName), []byte(fmt.Sprintf("timeout 1\n%s.exe", c.baseName)), os.ModePerm)
	// if err != nil {
	// 	fmt.Printf("Failed to write %s.bat: %s\n", c.baseName, err)
	// 	isErrored = true
	// }

	// c.logf("Writing log")
	// err = os.WriteFile(fmt.Sprintf("%s.txt", c.baseName), []byte(c.cacheLog), os.ModePerm)
	// if err != nil {
	// 	fmt.Println("Failed to write log:", err)
	// 	isErrored = true
	// }

	// cmd := c.createCommand(false, fmt.Sprintf("%s/%s.bat", c.currentPath, c.baseName))
	// cmd.Dir = c.currentPath
	// err = cmd.Start()
	// if err != nil {
	// 	fmt.Printf("Failed to start %s.bat: %s\n", c.baseName, err)
	// 	isErrored = true
	// }

	// if isErrored && runtime.GOOS == "windows" {
	// 	fmt.Printf("There was an error while self updating %s. Review above or %s.txt to see why.\n", c.baseName, c.baseName)
	// 	fmt.Println("Automatically exiting in 10 seconds...")
	// 	time.Sleep(10 * time.Second)
	// 	os.Exit(1)
	// }

	// c.logf("Successfully updated. Restarting %s and starting EverQuest...", c.baseName)
	// time.Sleep(1 * time.Second)
	// os.Exit(0)
	return nil
}

func (c *Client) logf(format string, a ...interface{}) {
	text := fmt.Sprintf(format, a...)
	text += "\n"
	fmt.Print(text)
	c.cacheLog += text
}

func (c *Client) patch() error {
	var err error
	start := time.Now()

	fileList := c.cacheFileList

	if c.cfg.FileListVersion == fileList.Version {
		if len(fileList.Version) < 8 {
			c.logf("We are up to date")
			return nil
		}
		c.logf("We are up to date latest patch %s", fileList.Version[0:8])
		return nil
	}

	totalSize := int64(0)

	for _, entry := range fileList.Downloads {
		totalSize += int64(entry.Size)
	}

	progressSize := int64(1)

	totalDownloaded := int64(0)

	if len(fileList.Version) < 8 {
		c.logf("Total patch size: %s", generateSize(int(totalSize)))
	} else {
		c.logf("Total patch size: %s, version: %s", generateSize(int(totalSize)), fileList.Version[0:8])
	}

	isMapsSkippedBefore := false
	for _, entry := range fileList.Downloads {
		if strings.Contains(entry.Name, "..") {
			c.logf("Skipping %s, has .. inside it", entry.Name)
			continue
		}

		if strings.Contains(entry.Name, "/") {
			newPath := strings.TrimSuffix(entry.Name, filepath.Base(entry.Name))
			err = os.MkdirAll(newPath, os.ModePerm)
			if err != nil {
				return fmt.Errorf("mkdir %s: %w", newPath, err)
			}
		}
		_, err := os.Stat(entry.Name)
		if err != nil {
			if os.IsNotExist(err) {
				err = c.downloadPatchFile(entry)
				if err != nil {
					return fmt.Errorf("download new file: %w", err)
				}
				totalDownloaded += int64(entry.Size)
				progressSize += int64(entry.Size)
				c.isPatched = true
				continue
			}
			return fmt.Errorf("stat %s: %w", entry.Name, err)
		}

		hash, err := md5Checksum(entry.Name)
		if err != nil {
			return fmt.Errorf("md5checksum: %w", err)
		}

		if hash == entry.Md5 {
			progressSize += int64(entry.Size)
			if strings.HasPrefix(strings.ToLower(entry.Name), "maps/") {
				if isMapsSkippedBefore {
					continue
				}
				isMapsSkippedBefore = true
			}
			c.logf("%s skipped (up to date)", entry.Name)
			continue
		}

		err = c.downloadPatchFile(entry)
		if err != nil {
			return fmt.Errorf("download new file: %w", err)
		}
		progressSize += int64(entry.Size)
		totalDownloaded += int64(entry.Size)
		c.isPatched = true
	}

	for _, entry := range fileList.Deletes {
		if strings.Contains(entry.Name, "..") {
			c.logf("Skipping %s, has .. inside it", entry.Name)
			continue
		}
		fi, err := os.Stat(entry.Name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", entry.Name, err)
		}
		if fi.IsDir() {
			c.logf("Skipping deleting %s, it is a directory", entry.Name)
			continue
		}
		err = os.Remove(entry.Name)
		if err != nil {
			c.logf("Failed to delete %s: %s", entry.Name, err)
			continue
		}
		c.logf("%s removed", entry.Name)
	}

	c.cfg.FileListVersion = fileList.Version
	err = c.cfg.Save()
	if err != nil {
		c.logf("Failed to save version to eqemupatch.yml: %s", err)
	}

	if totalDownloaded == 0 {
		c.patchSummary = fmt.Sprintf("Finished patch in %0.2f seconds", time.Since(start).Seconds())
		return nil
	}
	c.patchSummary = fmt.Sprintf("Finished patch of %s in %0.2f seconds", generateSize(int(totalDownloaded)), time.Since(start).Seconds())

	return nil
}

func (c *Client) downloadPatchFile(entry FileEntry) error {
	client := c.httpClient
	if !isMapsDownloaded && strings.HasPrefix(strings.ToLower(entry.Name), "maps/") {
		c.logf("Downloading maps.zip...")
		url := fmt.Sprintf("%s/maps.zip", c.patcherUrl)
		resp, err := client.Get(url)
		if err != nil {
			return fmt.Errorf("download %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("download %s responded %d (not 200)", url, resp.StatusCode)
		}

		w, err := os.Create("maps.zip")
		if err != nil {
			return fmt.Errorf("create %s: %w", entry.Name, err)
		}
		defer w.Close()

		_, err = io.Copy(w, resp.Body)
		if err != nil {
			return fmt.Errorf("write %s: %w", entry.Name, err)
		}

		//unzip it
		err = unpack("maps.zip", ".")
		if err != nil {
			return fmt.Errorf("unzip %s: %w", entry.Name, err)
		}

		isMapsDownloaded = true
		return nil
	}
	c.logf("%s (%s)", entry.Name, generateSize(entry.Size))

	w, err := os.Create(entry.Name)
	if err != nil {
		return fmt.Errorf("create %s: %w", entry.Name, err)
	}
	defer w.Close()

	url := fmt.Sprintf("%s/%s/%s", c.cacheFileList.DownloadPrefix, c.clientVersion, entry.Name)
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s responded %d (not 200)", url, resp.StatusCode)
	}

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("write %s: %w", entry.Name, err)
	}
	return nil
}

func md5Checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	_, err = io.Copy(h, f)
	if err != nil {
		return "", fmt.Errorf("new: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func generateSize(in int) string {
	val := float64(in)
	if val < 1024 {
		return fmt.Sprintf("%0.2f bytes", val)
	}
	val /= 1024
	if val < 1024 {
		return fmt.Sprintf("%0.2f KB", val)
	}
	val /= 1024
	if val < 1024 {
		return fmt.Sprintf("%0.2f MB", val)
	}
	val /= 1024
	if val < 1024 {
		return fmt.Sprintf("%0.2f GB", val)
	}
	val /= 1024
	return fmt.Sprintf("%0.2f TB", val)
}

func (c *Client) fetchUsername() (string, error) {

	r, err := os.Open("eqlsPlayerData.ini")
	if err != nil {
		return "", err
	}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	// optionally, resize scanner's capacity for lines over 64K, see next example
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Username=") {
			line = strings.TrimPrefix(line, "Username=")
			return line, nil
		}
	}
	return "", nil
}

// unpack unzips the provided path
func unpack(srcFile string, dstDir string) error {
	ext := filepath.Ext(srcFile)
	if ext != ".zip" {
		return fmt.Errorf("invalid extension: %s", ext)
	}
	r, err := zip.OpenReader(srcFile)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		filePath := filepath.Join(dstDir, f.Name)
		if f.FileInfo().IsDir() {
			err := os.MkdirAll(filePath, os.ModePerm)
			if err != nil {
				return fmt.Errorf("mkdirall: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
			return fmt.Errorf("mkdirall: %w", err)
		}

		outFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return fmt.Errorf("openfile: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open: %w", err)
		}

		_, err = io.Copy(outFile, rc)
		if err != nil {
			return fmt.Errorf("copy: %w", err)
		}

		outFile.Close()
		rc.Close()
	}

	return nil
}

// Exit closes the client
func Exit(sig int) {
	if !mousetrap.StartedByExplorer() {
		os.Exit(sig)
	}
	fmt.Println("Close this window or press CTRL+C to exit.")
	for {
		time.Sleep(1 * time.Second)
	}
}
