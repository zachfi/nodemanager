package jail

// zfs create -o mountpoint=/usr/local/jails zroot/jails
// zfs create zroot/jails/media
// zfs create zroot/jails/templates
// zfs create zroot/jails/containers

// fetch https://download.freebsd.org/ftp/releases/amd64/amd64/14.4-RELEASE/base.txz -o /usr/local/jails/media/14.4-RELEASE-base.txz
// mkdir -p /usr/local/jails/containers/classic
// tar -xf /usr/local/jails/media/14.4-RELEASE-base.txz -C /usr/local/jails/containers/classic --unlink

// cp /etc/resolv.conf /usr/local/jails/containers/classic/etc/resolv.conf
// cp /etc/localtime /usr/local/jails/containers/classic/etc/localtime

// freebsd-update -b /usr/local/jails/containers/classic/ fetch install

// Create the jail configuration file

// service jail start classic
