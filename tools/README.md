这里提供了两个脚本：
1. image-package.sh  主要是将我们所需要的 koordinator 镜像打包成一个 tar 文件,所需要打包的文件放在当前目录下的 images.txt 文件中，执行命名

``` sh
bash ./image-package.sh
```

2.  docker-push-to-harbor.sh 是将打包的 tar 包镜像 push 到镜像仓库中，首先需要确保镜像仓库中 hybrid-system 仓库组，执行命名示例

``` sh
    bash ./docker-push-to-harbor.sh 192.168.1.2/hybrid-system
```
就是将镜像 push 到 harbor 192.168.1.2 的 hybrid-system 仓库组中

