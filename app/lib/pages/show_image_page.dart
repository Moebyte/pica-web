import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:photo_view/photo_view.dart';
import 'package:pica_comic/components/image_actions.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/foundation/app.dart';
import 'package:pica_comic/foundation/image_loader/cached_image.dart';
import 'package:pica_comic/foundation/image_loader/image_bytes.dart';
import 'package:pica_comic/foundation/image_manager.dart';
import 'package:pica_comic/tools/file_type.dart';
import 'package:pica_comic/tools/save_image.dart';
import 'package:pica_comic/tools/translations.dart';
import 'package:pica_comic/web_compat/image_clipboard.dart';

Future<Uint8List?> _loadImageBytes(String url) async {
  final cached = ImageByteCache.get(url);
  if (cached != null) {
    return cached;
  }
  Uint8List? bytes;
  await for (final progress in ImageManager().getImage(url)) {
    if (progress.data != null) {
      bytes = progress.data;
    }
  }
  return bytes;
}

String _fileNameFromUrl(String url) {
  var path = Uri.tryParse(url)?.path ?? url;
  var name = path.split('/').last;
  if (name.isEmpty) name = "image";
  return name.contains('.') ? name.substring(0, name.lastIndexOf('.')) : name;
}

Widget _withImageActions(BuildContext context, String url, Widget child) {
  if (!App.isWeb) {
    return child;
  }
  return GestureDetector(
    behavior: HitTestBehavior.opaque,
    onLongPress: () => showImageActionsSheet(
      context,
      title: "图片".tl,
      fileName: _fileNameFromUrl(url),
      loadBytes: () => _loadImageBytes(url),
    ),
    child: child,
  );
}

class ShowImagePage extends StatelessWidget {
  const ShowImagePage(this.url, {super.key});

  final String url;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
        appBar: AppBar(
          title: Text("图片".tl),
        ),
        body: _withImageActions(
          context,
          url,
          PhotoView(
            minScale: PhotoViewComputedScale.contained * 0.9,
            imageProvider: CachedImageProvider(url),
            loadingBuilder: (context, event) {
              return Container(
                decoration: const BoxDecoration(color: Colors.black),
                child: const Center(
                  child: CircularProgressIndicator(),
                ),
              );
            },
          ),
        ));
  }
}

class ShowImagePageWithHero extends StatelessWidget {
  const ShowImagePageWithHero(this.url, this.tag, {super.key});

  final String url;

  final String tag;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text("图片".tl),
        actions: [
          Tooltip(
              message: "保存".tl,
              child: IconButton(
                  icon: const Icon(Icons.download),
                  onPressed: () async {
                    if (App.isWeb) {
                      final bytes = await _loadImageBytes(url);
                      if (bytes == null) {
                        showToast(message: "图片尚未加载完成，请稍后重试".tl);
                        return;
                      }
                      final type = detectFileType(bytes);
                      final ext = type.ext.length > 1 ? type.ext : ".jpg";
                      downloadImageBytes(
                          bytes, "${_fileNameFromUrl(url)}$ext", type.mime);
                      showToast(message: "已保存".tl);
                      return;
                    }
                    var file = await ImageManager().getFile(url);
                    if (file != null) {
                      saveImage(file);
                    }
                  }))
        ],
      ),
      body: _withImageActions(
          context,
          url,
          Hero(
            tag: tag,
            child: PhotoView(
              minScale: PhotoViewComputedScale.contained * 0.9,
              imageProvider: CachedImageProvider(url),
              loadingBuilder: (context, event) {
                return Container(
                  decoration: const BoxDecoration(color: Colors.black),
                  child: const Center(
                    child: CircularProgressIndicator(),
                  ),
                );
              },
            ),
          )),
    );
  }
}
