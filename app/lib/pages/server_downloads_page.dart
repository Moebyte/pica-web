/// 服务端下载页（Web 专属）。
///
/// Web 端没有本地下载，"已下载"改为浏览网关 `/api/downloads/*` 上的
/// 任务与已下载的漫画：
/// - 任务列表每 5 秒轮询一次；
/// - done / partial 的任务可点开逐页浏览（PageView + 懒加载）；
/// - 长按或右键条目可删除任务（可连同文件，二次确认）。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/foundation/widget_utils.dart';
import 'package:pica_comic/network/server_downloads.dart';
import 'package:pica_comic/tools/translations.dart';

/// 服务端下载任务列表页
class ServerDownloadsPage extends StatefulWidget {
  const ServerDownloadsPage({super.key});

  @override
  State<ServerDownloadsPage> createState() => _ServerDownloadsPageState();
}

class _ServerDownloadsPageState extends State<ServerDownloadsPage> {
  Timer? _timer;
  List<ServerDownloadJob>? _jobs;
  String? _error;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _load();
    _timer = Timer.periodic(const Duration(seconds: 5), (_) => _load());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    if (_loading) {
      return;
    }
    _loading = true;
    var res = await ServerDownloads.getJobs();
    _loading = false;
    if (!mounted) {
      return;
    }
    setState(() {
      if (res.success) {
        _jobs = res.data;
        _error = null;
      } else {
        _error = res.errorMessageWithoutNull;
      }
    });
  }

  void _showMenu(ServerDownloadJob job) {
    showModalBottomSheet(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.delete_outline),
              title: Text("删除任务".tl),
              subtitle: Text("保留已下载的文件".tl),
              onTap: () => _delete(job, false),
            ),
            ListTile(
              leading: const Icon(Icons.delete_forever_outlined),
              title: Text("删除任务和文件".tl),
              subtitle: Text("同时删除服务器上的图片".tl),
              onTap: () => _delete(job, true),
            ),
            ListTile(
              leading: const Icon(Icons.close),
              title: Text("取消".tl),
              onTap: () => Navigator.of(context).pop(),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _delete(ServerDownloadJob job, bool files) async {
    Navigator.of(context).pop();
    if (files) {
      // 二次确认
      var confirmed = await showDialog<bool>(
            context: context,
            builder: (context) => AlertDialog(
              title: Text("删除任务和文件".tl),
              content: Text(
                "将删除「${job.title}」的任务记录与服务器上已下载的 "
                "${job.total} 个文件，无法恢复。".tl,
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(context).pop(false),
                  child: Text("取消".tl),
                ),
                FilledButton(
                  onPressed: () => Navigator.of(context).pop(true),
                  child: Text("删除".tl),
                ),
              ],
            ),
          ) ??
          false;
      if (!confirmed) {
        return;
      }
    }
    var res = await ServerDownloads.deleteJob(job.id, files: files);
    if (!mounted) {
      return;
    }
    showToast(
        message: res.success
            ? "已删除".tl
            : "删除失败: ${res.errorMessageWithoutNull}".tl);
    _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text("服务端下载".tl),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _load,
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_jobs == null && _error == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && _jobs == null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text("加载失败".tl),
            const SizedBox(height: 8),
            Text(_error!, style: const TextStyle(fontSize: 13)),
            const SizedBox(height: 16),
            FilledButton.tonal(onPressed: _load, child: Text("重试".tl)),
          ],
        ),
      );
    }
    var jobs = _jobs!;
    if (jobs.isEmpty) {
      return Center(
        child: Text(
          "暂无服务端下载任务\n在漫画详情页点击\"下载\"提交任务".tl,
          textAlign: TextAlign.center,
          style: const TextStyle(fontSize: 14),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        itemCount: jobs.length,
        itemBuilder: (context, index) {
          var job = jobs[index];
          return _JobTile(
            job: job,
            onTap: job.isBrowsable
                ? () => context.to(() => ServerDownloadBrowserPage(job: job))
                : null,
            onMenu: () => _showMenu(job),
          );
        },
      ),
    );
  }
}

class _JobTile extends StatelessWidget {
  const _JobTile({required this.job, this.onTap, required this.onMenu});

  final ServerDownloadJob job;
  final VoidCallback? onTap;
  final VoidCallback onMenu;

  @override
  Widget build(BuildContext context) {
    var colorScheme = Theme.of(context).colorScheme;
    Color statusColor = switch (job.status) {
      "done" => colorScheme.primary,
      "partial" => Colors.orange,
      "error" => colorScheme.error,
      _ => colorScheme.outline,
    };

    var subtitle = job.epName.isNotEmpty ? job.epName : "全一话";
    var progress = job.total > 0 ? job.done / job.total : 0.0;

    return GestureDetector(
      // 长按 / 右键呼出删除菜单
      onLongPress: onMenu,
      onSecondaryTapUp: (_) => onMenu(),
      child: ListTile(
        onTap: onTap,
        leading: Icon(
          job.isBrowsable ? Icons.menu_book_outlined : Icons.downloading,
          color: job.isBrowsable ? colorScheme.primary : colorScheme.outline,
        ),
        title: Text(
          job.title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const SizedBox(height: 2),
            Text(
              "$subtitle · ${job.done}/${job.total}${job.failed > 0 ? " (失败 ${job.failed})" : ""}",
              style: const TextStyle(fontSize: 12),
            ),
            const SizedBox(height: 4),
            LinearProgressIndicator(
              value: job.status == "queued" ? null : progress,
              minHeight: 3,
            ),
          ],
        ),
        isThreeLine: false,
        trailing: Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          decoration: BoxDecoration(
            color: statusColor.withValues(alpha: 0.12),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text(
            job.statusText,
            style: TextStyle(fontSize: 12, color: statusColor),
          ),
        ),
      ),
    );
  }
}

/// 已下载漫画的浏览页：逐页展示任务目录下的图片
class ServerDownloadBrowserPage extends StatefulWidget {
  const ServerDownloadBrowserPage({super.key, required this.job});

  final ServerDownloadJob job;

  @override
  State<ServerDownloadBrowserPage> createState() =>
      _ServerDownloadBrowserPageState();
}

class _ServerDownloadBrowserPageState extends State<ServerDownloadBrowserPage> {
  /// 每页的文件名（001.jpg ...），解析成功前为 null
  List<String>? _files;

  /// 已探测过的文件名 -> 是否存在，避免重复请求
  final Map<String, bool> _probeCache = {};

  static const _extCandidates = ["jpg", "png", "webp", "gif"];

  int _page = 0;

  final _controller = PageController();

  ServerDownloadJob get job => widget.job;

  @override
  void initState() {
    super.initState();
    _resolveFiles();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  /// 页面文件命名约定为 `<序号3位>.<ext>`
  static String _fileName(int page, String ext) =>
      "${(page + 1).toString().padLeft(3, '0')}.$ext";

  Future<void> _resolveFiles() async {
    // 1) jobs API 直接带的 files 列表（网关 ≥20261031 起）
    if (job.files.isNotEmpty) {
      if (mounted) {
        setState(() => _files = List<String>.from(job.files));
      }
      return;
    }
    // 2) 读网关写出的 job.json（含 files 列表）
    var manifest = await ServerDownloads.readJobManifest(job.dir);
    if (manifest != null) {
      if (mounted) {
        setState(() => _files = manifest);
      }
      return;
    }
    // 3) 都没有时逐页探测扩展名（job.total 推文件名）
    var total = job.total > 0 ? job.total : job.done;
    if (total <= 0) {
      if (mounted) {
        setState(() => _files = []);
      }
      return;
    }
    var files = <String>[];
    for (var i = 0; i < total; i++) {
      var file = await _probeFile(i);
      if (file == null) {
        break;
      }
      files.add(file);
    }
    if (mounted) {
      setState(() => _files = files);
    }
  }

  /// 依次尝试 jpg/png/webp/gif，返回第一个存在的文件名
  Future<String?> _probeFile(int page) async {
    for (var ext in _extCandidates) {
      var name = _fileName(page, ext);
      var cacheKey = "$name@${job.dir}";
      if (_probeCache.containsKey(cacheKey)) {
        if (_probeCache[cacheKey]!) {
          return name;
        }
        continue;
      }
      var exists = await ServerDownloads.probeFile(job.dir, name);
      _probeCache[cacheKey] = exists;
      if (exists) {
        return name;
      }
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          "${job.title}${job.epName.isNotEmpty ? " · ${job.epName}" : ""}",
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: _buildBody(),
      bottomNavigationBar: _files != null && _files!.isNotEmpty
          ? BottomAppBar(
              child: Center(
                child: Text("${_page + 1} / ${_files!.length}"),
              ),
            )
          : null,
    );
  }

  Widget _buildBody() {
    var files = _files;
    if (files == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (files.isEmpty) {
      return Center(child: Text("没有找到已下载的文件".tl));
    }
    return PageView.builder(
      controller: _controller,
      itemCount: files.length,
      onPageChanged: (i) => setState(() => _page = i),
      itemBuilder: (context, index) {
        var url = ServerDownloads.fileUrl(job.dir, files[index]);
        return InteractiveViewer(
          maxScale: 5,
          child: Center(
            child: Image.network(
              url,
              fit: BoxFit.contain,
              errorBuilder: (context, error, stackTrace) {
                return Icon(
                  Icons.broken_image_outlined,
                  size: 48,
                  color: Theme.of(context).colorScheme.outline,
                );
              },
              loadingBuilder: (context, child, progress) {
                if (progress == null) {
                  return child;
                }
                return Center(
                  child: CircularProgressIndicator(
                    value: progress.expectedTotalBytes != null
                        ? progress.cumulativeBytesLoaded /
                            progress.expectedTotalBytes!
                        : null,
                  ),
                );
              },
            ),
          ),
        );
      },
    );
  }
}
