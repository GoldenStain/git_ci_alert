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

func getCIStatus(owner, repo string, ref string) ([]*github.CheckRun, error) {
	log.Print("args: ", owner, " ", repo, " ", ref)

	// 检查 client 是否为 nil
	if client == nil {
		log.Print("GitHub client is not initialized")
		return nil, fmt.Errorf("GitHub client is not initialized")
	}

	checkRuns, resp, err := client.Checks.ListCheckRunsForRef(context.Background(), owner, repo, ref, nil)
	if err != nil {
		log.Printf("Error fetching check runs: %v", err)
		return nil, err
	}

	if resp.StatusCode != 200 {
		log.Printf("Unexpected status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("Unexpected status code: %d", resp.StatusCode)
	}

	if checkRuns == nil || len(checkRuns.CheckRuns) == 0 {
		log.Print("No check runs found")
		return nil, fmt.Errorf("No check runs found")
	}

	log.Printf("Found %d check runs for ref %s", len(checkRuns.CheckRuns), ref)
	for _, checkRun := range checkRuns.CheckRuns {
		log.Printf("CheckRun: %s, Status: %s, Conclusion: %s", checkRun.GetName(), checkRun.GetStatus(), checkRun.GetConclusion())
	}

	return checkRuns.CheckRuns, nil
}

func getCIStatusUsingStatusAPI(owner, repo, ref string) (map[string]*github.RepoStatus, error) {
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

	if statuses == nil || len(statuses) == 0 {
		log.Print("No statuses found")
		return nil, fmt.Errorf("No statuses found")
	}

	log.Printf("Found %d statuses for ref %s", len(statuses), ref)

	latestStatuses := make(map[string]*github.RepoStatus)
	for _, status := range statuses {
		context := status.GetContext()
		if existingStatus, exists := latestStatuses[context]; !exists || status.GetUpdatedAt().Time.After(existingStatus.GetUpdatedAt().Time) {
			latestStatuses[context] = status
		}
	}

	// for context, status := range latestStatuses {
	// 	log.Printf("Status: %s, State: %s", context, status.GetState())
	// }

	return latestStatuses, nil
}

func checkCIForPR(owner, repo string, pr *github.PullRequest) {
	statuses, err := getCIStatusUsingStatusAPI(owner, repo, pr.Head.GetSHA())
	if err != nil {
		log.Printf("Error getting CI status for PR #%d: %v", *pr.Number, err)
	}

	// 这里可以添加更多的逻辑来检查 statuses 的状态
	for context, status := range statuses {
		if status.GetState() == "failure" && status.GetContext() != "PR-CI-Kunlun-R200" {
			log.Print("CI failed for PR: ", *pr.Number)
			alertFailure(*pr.Number, pr.GetTitle(), context)
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
			checkCIForPR(owner, repo, pr)
		}

		time.Sleep(60 * time.Second)
	}
}

func main() {
	initClient()
	owner := "PaddlePaddle"
	repo := "Paddle"
	creator := "GoldenStain"
	monitorPRs(owner, repo, creator)
}
