package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func PathExists(path string) (bool, bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return true, info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, false, nil
	}
	return false, false, err
}

func InArray(value string, arr []string) bool {

	var result = false

	for _, v := range arr {
		if v == value {
			result = true
			break
		}
	}
	return result
}

func FindRecentModifyFile(fileName string, interval int64, timestamp int64, noWatch []string, result *[]string) []string {

	list, err := ioutil.ReadDir(fileName)
	if err != nil {
		fmt.Println("err = ", err)
		return *result
	}

	for _, fi := range list {
		currentFile := fi.Name()
		currentFile = fileName + string(os.PathSeparator) + fi.Name()

		if InArray(fi.Name(), noWatch) {
			continue
		}

		if fi.IsDir() {
			FindRecentModifyFile(currentFile, interval, timestamp, noWatch, result)
			continue
		}

		//当 interval 为 0 时，上传所有文件
		if timestamp-fi.ModTime().Unix() < interval || interval == 0 {
			*result = append(*result, currentFile)
		}
	}
	return *result
}

func MkDir(dst string) (int64, error) {
	dstArr := strings.Split(dst, string(os.PathSeparator))
	dstLen := len(dstArr)
	fPath := string("")
	if dstLen > 1 {
		for key, temp := range dstArr {

			if key == dstLen-1 {
				break
			}

			if fPath == "" {
				fPath = temp
			} else {
				fPath = fPath + string(os.PathSeparator) + temp
			}

			exist, isDir, err := PathExists(fPath)
			if err != nil {
				return 0, err
			}

			if exist || isDir {
				continue
			}

			if !exist {
				err := os.Mkdir(fPath, os.ModePerm)
				if err != nil {
					return 0, err
				}
			}
		}
	}
	return 0, nil
}

func CopyFile(src, dst string) (int64, error) {

	_, err := MkDir(dst)
	if err != nil {
		fmt.Print(err)
	}

	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()
	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

// 初始化配置
func InitConf(conf string, cache *map[string]map[string]map[string]string) {

	confPath := GetConfPath() + string(os.PathSeparator) + conf + ".ini"

	f, err := os.Open(confPath)
	if err != nil {
		panic(err)
	}

	defer f.Close()

	rd := bufio.NewReader(f)

	section := string("")
	var content []string
	for {
		line, err := rd.ReadString('\n') //以'\n'为结束符读入一行

		if err != nil || io.EOF == err {
			break
		}

		line = strings.Trim(line, " ")
		line = strings.Trim(line, "\n")
		if len(line) == 0 {
			continue
		}

		//跳过注释行
		if line[0] == '#' {
			continue
		}

		if line[0] == '[' {
			line = strings.Trim(line, "[")
			line = strings.Trim(line, "]")
			section = line
			continue
		}

		if section == "" {
			continue
		}

		content = strings.Split(line, "=")
		if len(content) < 2 {
			continue
		}

		key := strings.Trim(content[0], " ")
		value := strings.Trim(content[1], " ")

		if _, ok := (*cache)[conf]; !ok {
			(*cache)[conf] = make(map[string]map[string]string)
		}
		if _, ok := (*cache)[conf][section]; !ok {
			(*cache)[conf][section] = make(map[string]string)
		}
		(*cache)[conf][section][key] = value
	}
}

// 获取deployd的运行路径
func GetRunPath() string {
	file, _ := exec.LookPath(os.Args[0])
	deployPath, _ := filepath.Abs(file)
	deployHome := filepath.Dir(deployPath)
	return deployHome
}

//获取输出目录
func GetOutputPath() string {

	return os.Getenv("PWD") + string(os.PathSeparator) + conf["watch"]["global"]["build_dir"] +
		string(os.PathSeparator) + conf["watch"]["global"]["output_dir"]
}

//获取编译目录
func GetBuildPath() string {
	return os.Getenv("PWD") + string(os.PathSeparator) + conf["watch"]["global"]["build_dir"]
}

//获取上传文件的目标路径
func GetUploadUrl() string {
	machine := conf["deploy"][*desc]["machine"]
	arr := strings.Split(machine, "@")
	return strings.Replace(conf["watch"]["global"]["fis_server"], "fis_server", arr[1], -1)
}

//获取配置目录
func GetConfPath() string {
	return GetRunPath() + string(os.PathSeparator) + "conf"
}

func BuildAddUpLoad() {

	compile := GetRunPath() + string(os.PathSeparator) + "deps" + string(os.PathSeparator) + "compile.sh"

	// 开始编译
	cmd := exec.Command("bash", compile)
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Execute Command failed:" + err.Error())
		return
	}
	fmt.Println(string(output))

	// 通过fis上传
	UploadByFis(GetOutputPath())

	err = os.RemoveAll(GetBuildPath())
	if err != nil {
		fmt.Println(err)
	}
}

func UploadFile(filename string, targetUrl string, params map[string]string) error {

	bodyBuf := bytes.NewBufferString("")
	bodyWriter := multipart.NewWriter(bodyBuf)

	err := bodyWriter.SetBoundary(bodyWriter.Boundary())
	if err != nil {
		fmt.Println(err)
		return err
	}

	for key, value := range params {
		err = bodyWriter.WriteField(key, value)
		if err != nil {
			fmt.Println(err)
			return err
		}
	}

	_, err = bodyWriter.CreateFormFile("file", filename)
	if err != nil {
		fmt.Println(err)
		return err
	}
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		fmt.Println(err)
		return err
	}

	_, err = bodyBuf.Write(file)
	if err != nil {
		fmt.Println(err)
		return err
	}

	_ = bodyWriter.Close()

	reqReader := io.MultiReader(bodyBuf)
	req, err := http.NewRequest("POST", targetUrl, reqReader)
	if err != nil {
		fmt.Println(err)
		return err
	}
	// 添加Post头
	req.Header.Set("Connection", "close")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Content-Type", bodyWriter.FormDataContentType())
	req.ContentLength = int64(bodyBuf.Len())

	// 发送消息
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("读取回应消息异常:", err)
		return err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("读取回应消息异常:", err)
	}
	fmt.Println("发送回应数据:", string(body))
	return nil
}

func Md5SumFile(file string) (value [md5.Size]byte, err error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return
	}
	value = md5.Sum(data)
	return value, nil
}

func MD5Bytes(s []byte) string {
	ret := md5.Sum(s)
	return hex.EncodeToString(ret[:])
}

//计算字符串MD5值
func MD5(s string) string {
	return MD5Bytes([]byte(s))
}

//计算文件MD5值
func MD5File(file string) (string, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return "", err
	}
	return MD5Bytes(data), nil
}


func GetSign(filepath string) string {
	return MD5(signSecret + filepath)
}


func UploadByFis(path string) {
	list, err := ioutil.ReadDir(path)
	if err != nil {
		fmt.Println("err = ", err)
		return
	}

	odpPath := conf["deploy"]["global"]["odp_path"]
	outputPath := GetOutputPath()
	for _, fi := range list {
		params := make(map[string]string)

		if fi == nil {
			continue
		}

		if fi.IsDir() {
			UploadByFis(path + string(os.PathSeparator) + fi.Name())
			continue
		}

		from := path + string(os.PathSeparator) + fi.Name()
		to := strings.Replace(from, outputPath, odpPath, -1)
		params["to"] = to
		params["sign"] = GetSign(to)

		// 异步上传
		wg.Add(1)
		go func(from string, params map[string]string) {
			//fmt.Println(from, params)
			err := UploadFile(from, GetUploadUrl(), params)
			if err != nil {
				fmt.Println(err)
			}
			wg.Done()
		}(from, params)
	}
	wg.Wait()
}

func FindAndCompile(buildDir string, interval int64, uploadStatus map[string]int64, noWatch []string, result []string) bool {

	timestamp := time.Now().Unix()
	FindRecentModifyFile(*watchPath, interval, timestamp, noWatch, &result)

	if len(result) == 0 {
		return false
	}

	for _, file := range result {

		if uploadStatus[file] == timestamp {
			continue
		}

		uploadStatus[file] = timestamp
		fmt.Println("发现被变更的文件：", file, "开始编译上传")

		if exist, _, _ := PathExists(buildDir); !exist {
			err := os.Mkdir(buildDir, os.ModePerm)
			if err != nil {
				fmt.Println(err)
			}
		}

		_, err := CopyFile(file, buildDir+string(os.PathSeparator)+file)
		if err != nil {
			fmt.Println(err)
		}
	}
	result = []string{}

	if exist, _, _ := PathExists(buildDir); !exist {
		return false
	}

	_, err := CopyFile("build.sh", buildDir+string(os.PathSeparator)+"build.sh")
	if err != nil {
		fmt.Println(err)
		return false
	}

	BuildAddUpLoad()
	return true
}

func InitAllConf(conf *map[string]map[string]map[string]string) {
	list, err := ioutil.ReadDir(GetConfPath())
	if err != nil {
		fmt.Println("err = ", err)
		return
	}

	for _, fi := range list {
		if path.Ext(fi.Name()) == ".ini" {
			name := strings.Replace(fi.Name(), ".ini", "", -1)
			InitConf(name, conf)
		}
	}
}



func ReceiveFileHandler(w http.ResponseWriter, r *http.Request) {
	//设置内存大小
	r.ParseMultipartForm(32 << 20)

	// 获取部署路径
	to := r.PostFormValue("to")
	sign := r.PostFormValue("sign")

	if sign != GetSign(to) {
		fmt.Fprint(w, "sign error:" + GetSign(to) + ":" + to )
		return
	}


	formFile, _, err := r.FormFile("file")
	if err != nil {
		fmt.Printf("Get form file failed: %s\n", err)
		fmt.Fprint(w, err)
		return
	}
	defer formFile.Close()

	_, err = MkDir(to)
	if err != nil {
		fmt.Print(err)
		fmt.Fprint(w, err)
	}

	// 创建保存文件
	destFile, err := os.Create(to)
	if err != nil {
		log.Printf("Create failed: %s\n", err)
		fmt.Fprint(w, err)
		return
	}

	defer destFile.Close()

	_, err = io.Copy(destFile, formFile)
	if err != nil {
		log.Printf("Write file failed: %s\n", err)
		fmt.Fprint(w, err)
		return
	}
	fmt.Fprint(w, "ok..")
}


func startDeploydServer(listen string){
	signSecret = conf["watch"]["global"]["secret"]
	http.HandleFunc("/upload", ReceiveFileHandler)
	http.ListenAndServe(listen, nil)
}

var conf = make(map[string]map[string]map[string]string)
var desc = flag.String("host", "", "指定目标主机")
var help = flag.Bool("help", false, "展示帮助信息")
var uploadAll = flag.Bool("all", false, "编译上传 -watch 选项指定目录下的所有文件")
var watchPath = flag.String("watch", ".", "指定要监听到发现变更后编译上传的目录")
var modifyTime = flag.Int64("time", -1, "上传 n 秒内被修改的文件")
var listen  =  flag.String("listen",  "", "启动一个server，监听目标端口，例如：-listen 0:8080")

var wg sync.WaitGroup
var signSecret string

func main() {

	icon := `
	Welcome to use :

		██████╗ ███████╗██████╗ ██╗      ██████╗ ██╗   ██╗██████╗
		██╔══██╗██╔════╝██╔══██╗██║     ██╔═══██╗╚██╗ ██╔╝██╔══██╗
		██║  ██║█████╗  ██████╔╝██║     ██║   ██║ ╚████╔╝ ██║  ██║
		██║  ██║██╔══╝  ██╔═══╝ ██║     ██║   ██║  ╚██╔╝  ██║  ██║
		██████╔╝███████╗██║     ███████╗╚██████╔╝   ██║   ██████╔╝
		╚═════╝ ╚══════╝╚═╝     ╚══════╝ ╚═════╝    ╚═╝   ╚═════╝
	`

	fmt.Println(icon)

	flag.Parse()

	// 存储上传状态
	var uploadStatus = make(map[string]int64)

	// 初始化配置
	InitAllConf(&conf)

	// 用于编译的缓存目录
	buildDir := conf["watch"]["global"]["build_dir"]

	// 不监听的文件名
	noWatch := strings.Split(conf["watch"]["global"]["no_watch"], ",")

	// 周期
	interval, err := strconv.ParseInt(conf["watch"]["global"]["interval"], 10, 64)
	if err != nil {
		fmt.Println(err)
	}

	//用于存储被修改的文件列表
	result := make([]string, 0)

	if *listen != "" {
		startDeploydServer(*listen)
		return
	}


	// 若 -help 或 未传入主机名 ，则展示帮助信息
	if *help || *desc == "" {
		flag.Usage()
		return
	}

	if _, ok := conf["deploy"][*desc]; !ok {
		fmt.Println("输入目标：", *desc, "不合法， 请重新输入")
		return
	}

	signSecret = conf["deploy"][*desc]["secret"]

	// 若是 -time 模式，上传n秒内被修改的文件
	if *modifyTime != -1 && *modifyTime > 0 {
		FindAndCompile(buildDir, *modifyTime, uploadStatus, noWatch, result)
		return
	}

	// 若是 -all 模式，则编译上传所有文件
	if *uploadAll {
		FindAndCompile(buildDir, 0, uploadStatus, noWatch, result)
		return
	}

	for {
		FindAndCompile(buildDir, interval, uploadStatus, noWatch, result)
	}
}
