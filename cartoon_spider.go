package main

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	site := "https://m.36mh.com/manhua/yiquanchaoren/" //注: 域名必须以m.开头
	downloadPath := "download"

	//site: 目标url, downloadPath: 下载目录, begin: 起始章节, end: 结束章节 , maxRoutineNum: 最大协程(建议1-16), mergeImage: 是否垂直合并图片
	start(site, downloadPath, 1, 16, 8, true)
}

var (
	mergeImg    bool
	downloadDir string
)

type Chapter struct {
	name string
	url  string
}

func start(site, downloadDirectory string, begin, end, maxRoutineNum int, mergeImage bool) {
	mergeImg = mergeImage
	downloadDir = downloadDirectory

	resp, err := http.Get(site)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	//获取漫画名称
	title := doc.Find("div.view-sub").Find("h1.title").Text()
	downloadDir += "/" + title + "/"

	path, err := filepath.Abs(downloadDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("DownloadDir:", path)
	time.Sleep(time.Second)

	ch := make(chan int, maxRoutineNum)
	chapterList := getChapterList(doc)
	log.Println(len(chapterList))

	var wg sync.WaitGroup
	for i, v := range chapterList {
		i++
		if i >= begin && (i <= end || end == -1) {
			ch <- 1 //ch缓冲满时将会阻塞
			wg.Add(1)
			go func(name, url string, id int, ch chan int) {
				downloadChapter(name, url, 1, ch)
				wg.Add(-1)
			}(v.name, v.url, 1, ch)
		}
	}

	wg.Wait()
}

func getChapterList(doc *goquery.Document) (list []Chapter) {
	doc.Find("ul.Drama").Children().Each(func(i int, selection *goquery.Selection) {
		v := selection.Find("a")
		url, _ := v.Attr("href")
		name := v.Find("span").Text()
		list = append(list, Chapter{name, url})
	})

	return list
}

var images = make(map[string][]*image.Image)

func downloadChapter(name, url string, id int, ch chan int) {
	log.Println("Get Chapter:", name, id, url)

	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	if resp.StatusCode != 200 {
		log.Fatal("Get chapter fail: StatusCode != 200")
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	//查找图片
	imgSelection := doc.Find("div.UnderPage").Children().Eq(2) //图片区域div索引
	imgUrl, exists := imgSelection.Find("mip-link>mip-img").Attr("src")
	if exists {
		fileName := fmt.Sprintf("%s-%d.jpg", name, id)
		downloadImage(imgUrl, fileName, name)
	}

	btnSelection := doc.Find("div.action-list>ul").Children().Eq(2) //按钮索引
	nextUrl, exists := btnSelection.Find("mip-link").Attr("href")
	if exists {
		if strings.HasSuffix(nextUrl, "html") { //非html结尾说明是首页
			id++
			downloadChapter(name, nextUrl, id, ch)
		} else { //爬取完整个章节
			log.Println(name, "爬取完毕")
			if mergeImg {
				img := mergeImage(images[name])
				writeImageToFile(img, fmt.Sprintf("%s/%s.jpg", downloadDir, name))
				delete(images, name)
				runtime.GC()
				debug.FreeOSMemory() //回收内存到系统 不然内存分分钟上GB
			}
			<-ch //空闲协程+1
		}
	}
}

//垂直合并图片
func mergeImage(images []*image.Image) (img *image.Image) {
	//计算合并后图片大小
	var size image.Point
	for _, vp := range images {
		v := *vp
		size = image.Point{X: v.Bounds().Dx(), Y: v.Bounds().Dy() + size.Y}
	}

	rgba := image.NewRGBA(image.Rectangle{Max: size})
	point := image.Pt(0, 0)
	for _, vp := range images {
		v := *vp

		//将要绘制的图片在背景图rgba 中的位置
		rect := image.Rectangle{Min: point, Max: image.Pt(v.Bounds().Size().X, point.Y+v.Bounds().Size().Y)}
		draw.Draw(rgba, rect, v, image.Pt(0, 0), draw.Src)

		//下一张图片绘制的位置
		point = image.Pt(0, point.Y+v.Bounds().Dy())
	}

	ptr := image.Image(rgba)
	return &ptr
}

func downloadImage(imgUrl, fileName, chapterName string) {
	log.Println("Download:", fileName, imgUrl)

	resp, err := http.Get(imgUrl)
	if err != nil {
		log.Println(err)
		downloadImage(imgUrl, fileName, chapterName)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Fatal(imgUrl, "Image download fail: StatusCode != 200")
	}

	img, err := jpeg.Decode(resp.Body)
	if err != nil {
		log.Fatal(imgUrl, err)
	}

	if mergeImg { //垂直合并图片
		//添加到map 当整个章节爬完后再合并
		value, ok := images[chapterName]
		if ok {
			images[chapterName] = append(value, &img) //追加
		} else {
			images[chapterName] = []*image.Image{&img}
		}
	} else { //不合并 直接保存到本地
		writeImageToFile(&img, downloadDir+"/"+chapterName+"/"+fileName)
	}
}

func writeImageToFile(img *image.Image, filePath string) {
	//目录不存在则创建
	err := os.MkdirAll(filepath.Dir(filePath), 0777)
	if err != nil && !os.IsExist(err) {
		log.Fatal(err)
	}

	//创建并写入img到文件
	file, err := os.Create(filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	err = jpeg.Encode(file, *img, &jpeg.Options{Quality: 80})
	if err != nil {
		log.Fatal(err)
	}
}
