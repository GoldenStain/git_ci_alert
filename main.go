package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/google/go-github/v52/github"
	"golang.org/x/oauth2"
)

var client *github.Client
var prStatusMap = make(map[int]bool) // 用于存储每个 PR 的状态

func initClient() {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN is required")
	}
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)
	client = github.NewClient(tc)
}

func getPRs(owner, repo, creator string) ([]*github.PullRequest, error) {
	since := time.Now().Add(-7 * 24 * time.Hour) // 7天内的PR

	var allPRs []*github.PullRequest
	options := &github.PullRequestListOptions{
		State: "open", // 开放的PR
		ListOptions: github.ListOptions{
			Page:    1,   // 开始的第一页
			PerPage: 100, // 每次请求最多100个PR
		},
	}

	flag := false

	for {
		// 获取当前页的PR
		prs, _, err := client.PullRequests.List(context.Background(), owner, repo, options)
		if err != nil {
			return nil, err
		}

		// 如果没有更多PR，退出循环
		if len(prs) == 0 {
			break
		}

		// 手动筛选符合条件的PR（近7天内）
		for _, pr := range prs {
			if pr.CreatedAt.Before(since) {
				flag = true
				break
			} else {
				if pr.User.GetLogin() == creator {
					allPRs = append(allPRs, pr)
				}
			}
		}

		if flag {
			break
		}

		// 如果当前页的PR数量少于PerPage，说明已经获取到所有PR
		if len(prs) < options.PerPage {
			break
		}

		// 增加页码，获取下一页
		options.ListOptions.Page++
	}

	return allPRs, nil
}

func getCIStatusUsingStatusAPI(owner, repo, ref string, latestStatuses *map[string]*github.RepoStatus) (map[string]*github.RepoStatus, error) {
	log.Print("args: ", owner, " ", repo, " ", ref)

	// 检查 client 是否为 nil
	if client == nil {
		log.Print("GitHub client is not initialized")
		return nil, fmt.Errorf("GitHub client is not initialized")
	}

	statuses, resp, err := client.Repositories.ListStatuses(context.Background(), owner, repo, ref, nil)
	if err != nil {
		log.Printf("Error fetching statuses: %v", err)
		return nil, err
	}

	if resp.StatusCode != 200 {
		log.Printf("Unexpected status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("Unexpected status code: %d", resp.StatusCode)
	}

	if len(statuses) == 0 {
		log.Print("No statuses found")
		return nil, fmt.Errorf("No statuses found")
	}

	log.Printf("Found %d statuses for ref %s", len(statuses), ref)

	for _, status := range statuses {
		context := status.GetContext()
		existingStatus, exists := (*latestStatuses)[context]
		if !exists {
			(*latestStatuses)[context] = status
		} else {
			if status.GetUpdatedAt().Time.After(existingStatus.GetUpdatedAt().Time) {
				(*latestStatuses)[context] = status
			}
		}
	}

	return *latestStatuses, nil
}

var notRequiredCIs = []string{
	"PR-CI-Kunlun-R200",
}

func checkNotRequired(CIname string) bool {
	isNotRequired := false
	for _, name := range notRequiredCIs {
		if CIname == name {
			isNotRequired = true
			break
		}
	}
	return isNotRequired
}

func checkCIForPR(owner, repo string, pr *github.PullRequest) bool {
	result := true

	latestStatuses := make(map[string]*github.RepoStatus)
	latestStatusesPtr := &latestStatuses

	statuses, err := getCIStatusUsingStatusAPI(owner, repo, pr.Head.GetSHA(), latestStatusesPtr)
	if err != nil {
		log.Printf("Error getting CI status for PR #%d: %v", *pr.Number, err)
	}

	// 这里可以添加更多的逻辑来检查 statuses 的状态
	for context, status := range statuses {
		if !checkNotRequired(status.GetContext()) && status.GetState() == "failure" {
			result = false
			log.Print("CI failed for PR: ", *pr.Number)
			alertFailure(*pr.Number, pr.GetTitle(), context)
		}
	}
	return result
}

func checkPRStatus(owner, repo string, pr *github.PullRequest) {
	detailedPR, _, err := client.PullRequests.Get(context.Background(), owner, repo, *pr.Number)
	if err != nil {
		log.Printf("Error getting PR details for PR #%d: %v", *pr.Number, err)
		return
	}

	prNumber := *pr.Number
	isMerged := detailedPR.GetMerged()

	// 检查 PR 的状态是否已记录或是否发生变化
	if prevState, exists := prStatusMap[prNumber]; !exists || prevState != isMerged {
		prStatusMap[prNumber] = isMerged
		if isMerged {
			log.Printf("PR #%d has been merged", prNumber)
			alertMerge(prNumber, pr.GetTitle())
		}
	}
}

func alertFailure(prNumber int, prName, context string) {
	title := fmt.Sprintf("PR #%d CI Failure", prNumber)
	message := fmt.Sprintf("CI: %s\nPR: %s", context, prName)
	group := fmt.Sprintf("PR-%d", prNumber) // 使用 PR 编号作为分组标识

	// 移除旧的通知
	removeCmd := exec.Command("terminal-notifier", "-remove", group)
	err := removeCmd.Run()
	if err != nil {
		fmt.Printf("Error removing old notification: %v\n", err)
	}

	// 发送新的通知
	for i := 0; i < 3; i++ { // 重复发送3次通知
		cmd := exec.Command("terminal-notifier", "-title", title, "-message", message, "-timeout", "10", "-sound", "default", "-group", group)
		err := cmd.Run()
		if err != nil {
			fmt.Printf("Error sending notification: %v\n", err)
		}
		time.Sleep(2 * time.Second) // 每次通知之间间隔1秒
	}
}

func alertMerge(prNumber int, prTitle string) {
	title := fmt.Sprintf("PR #%d Merged", prNumber)
	message := fmt.Sprintf("PR: %s", prTitle)
	group := fmt.Sprintf("PR-%d", prNumber) // 使用 PR 编号作为分组标识

	// 移除旧的通知
	removeCmd := exec.Command("terminal-notifier", "-remove", group)
	err := removeCmd.Run()
	if err != nil {
		fmt.Printf("Error removing old notification: %v\n", err)
	}

	// 发送新的通知
	cmd := exec.Command("terminal-notifier", "-title", title, "-message", message, "-timeout", "10", "-sound", "default", "-group", group)
	err = cmd.Run()
	if err != nil {
		fmt.Printf("Error sending notification: %v\n", err)
	}
}

func monitorPRs(owner, repo, creator string) {
	prs, err := getPRs(owner, repo, creator)
	log.Print("getPRs done")
	if err != nil {
		log.Printf("Error fetching PRs: %v", err)
		return
	}

	for {
		for _, pr := range prs {
			log.Printf("Checking PR #%d", *pr.Number)
			needtoCheckStatus := checkCIForPR(owner, repo, pr)
			if needtoCheckStatus {
				checkPRStatus(owner, repo, pr) // 检查 PR 状态
			}
		}

		time.Sleep(360 * time.Second)
	}
}

func main() {
	initClient()
	owner := "PaddlePaddle"
	repo := "Paddle"
	creator := "GoldenStain"
	monitorPRs(owner, repo, creator)
}
