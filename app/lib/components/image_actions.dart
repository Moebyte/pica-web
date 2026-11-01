import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/tools/file_type.dart';
import 'package:pica_comic/tools/translations.dart';
import 'package:pica_comic/web_compat/image_clipboard.dart';

/// Bottom sheet shown on a long-press over an image.
///
/// [loadBytes] is called only when the user picks an action, so the sheet opens
/// immediately even if the image still has to be re-downloaded.
Future<void> showImageActionsSheet(
  BuildContext context, {
  required Future<Uint8List?> Function() loadBytes,
  required String fileName,
  String? title,
}) async {
  final action = await showModalBottomSheet<String>(
    context: context,
    showDragHandle: true,
    builder: (context) {
      return SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (title != null && title.isNotEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 4, 20, 8),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                ),
              ),
            ListTile(
              leading: const Icon(Icons.copy_all_outlined),
              title: Text("复制图片".tl),
              subtitle: Text("复制到系统剪贴板".tl),
              onTap: () => Navigator.pop(context, "copy"),
            ),
            ListTile(
              leading: const Icon(Icons.download_outlined),
              title: Text("保存图片".tl),
              subtitle: Text("下载到本地".tl),
              onTap: () => Navigator.pop(context, "save"),
            ),
            const SizedBox(height: 8),
          ],
        ),
      );
    },
  );
  if (action == null) return;

  final bytes = await loadBytes();
  if (bytes == null || bytes.isEmpty) {
    showToast(message: "图片尚未加载完成，请稍后重试".tl);
    return;
  }
  final type = detectFileType(bytes);
  final ext = type.ext.length > 1 ? type.ext : ".jpg";
  final name = "${_safeFileName(fileName)}$ext";

  if (action == "save") {
    downloadImageBytes(bytes, name, type.mime);
    showToast(message: "已保存".tl);
    return;
  }

  showToast(message: "正在复制图片…".tl);
  final png = type.mime == "image/png" ? bytes : await _toPng(bytes);
  if (png == null) {
    showToast(message: "图片转换失败，请改用保存".tl);
    return;
  }
  final ok = await copyImageToClipboard(png, "image/png");
  showToast(message: ok ? "已复制图片".tl : "当前浏览器不支持复制图片，请改用保存".tl);
}

String _safeFileName(String value) {
  var name = value.replaceAll(RegExp(r'[\\/:*?"<>|\r\n\t]+'), '_').trim();
  if (name.isEmpty) name = "image";
  if (name.length > 80) name = name.substring(0, 80);
  return name;
}

Future<Uint8List?> _toPng(Uint8List bytes) async {
  ui.Codec? codec;
  ui.Image? image;
  try {
    codec = await ui.instantiateImageCodec(bytes);
    final frame = await codec.getNextFrame();
    image = frame.image;
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    return data?.buffer.asUint8List();
  } catch (_) {
    return null;
  } finally {
    image?.dispose();
    codec?.dispose();
  }
}
