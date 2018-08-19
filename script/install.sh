#!/bin/bash

set -e

echo "usage: ./install.sh [filebeat path] [filebeat config path] [filebeat lib path] [filebeat log path] [filebeat version]"

FILEBEAT_PATH=$1
FILEBEAT_CONFIG_PATH=$2
FILEBEAT_LIB_PATH=$3
FILEBEAT_LOG_PATH=$4
FILEBEAT_VERSION=$5

if [ "${FILEBEAT_PATH}" == "" ]; then
    FILEBEAT_PATH=/opt/log/filebeat
fi

if [ "${FILEBEAT_CONFIG_PATH}" == "" ]; then
    FILEBEAT_CONFIG_PATH=/opt/log/filebeat
fi

if [ "${FILEBEAT_LIB_PATH}" == "" ]; then
    FILEBEAT_LIB_PATH=/opt/log/filebeat/data
fi

if [ "${FILEBEAT_LOG_PATH}" == "" ]; then
    FILEBEAT_LOG_PATH=/opt/log/filebeat/log
fi

if [ "${FILEBEAT_VERSION}" == "" ]; then
    FILEBEAT_VERSION=6.2.4
fi

echo "filebeat path:${FILEBEAT_PATH}"
echo "filebeat config path:${FILEBEAT_CONFIG_PATH}"
echo "filebeat lib path:${FILEBEAT_LIB_PATH}"
echo "filebeat log path:${FILEBEAT_LOG_PATH}"
echo "filebeat version:${FILEBEAT_VERSION}"

mkdir -p ${FILEBEAT_CONFIG_PATH} ${FILEBEAT_LIB_PATH} ${FILEBEAT_LOG_PATH}

cd ${FILEBEAT_PATH}

tar -zxvf filebeat-${FILEBEAT_VERSION}-linux-x86_64.gz

cd filebeat-${FILEBEAT_VERSION}-linux-x86_64

cp -rf filebeat /usr/bin
cp -rf fields.yml ${FILEBEAT_CONFIG_PATH}
cp -rf kibana ${FILEBEAT_CONFIG_PATH}
cp -rf module ${FILEBEAT_CONFIG_PATH}
cp -rf modules.d ${FILEBEAT_CONFIG_PATH}

cd ..

rm -rf filebeat-${FILEBEAT_VERSION}-linux-x86_64

mkdir -p ${FILEBEAT_CONFIG_PATH}/prospectors.d

echo "install success!!"