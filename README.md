# git_ci_alert

## 依赖

设置环境变量`GITHUB_TOKEN`保存自己的token值，并且`homebrew`安装`terminal-notifier`工具。

## 功能

专门用于在mac OS上监测百度效率云的CI状态，如果发现有required的CI失败（目前手动指定，后续可能改成自动更新），就会发送醒目通知。

## 获取方式

利用`StatusAPI`获取效率云的CI状态，经检验发现，`CheckRunsAPI`无法获取相应的信息。