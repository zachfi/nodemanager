# Maintainer: Zach Leslie <@zachfi>

pkgname=nodemanager-bin
_realname=nodemanager
pkgver=0.1.34
pkgrel=1
pkgdesc='A Kubernetes controller to manage nodes'
url="https://github.com/zachfi/$_realname"
arch=(aarch64 armv7h x86_64)
license=('APACHE')
#source=("${_realname}-${pkgver}.tar.gz::https://github.com/zachfi/${_realname}/archive/refs/tags/v${pkgver}.tar.gz")
source_x86_64=("https://github.com/zachfi/${_realname}/releases/download/v${pkgver}/${_realname}_${pkgver}_linux_amd64.tar.gz")
source_aarch64=("https://github.com/zachfi/${_realname}/releases/download/v${pkgver}/${_realname}_${pkgver}_linux_arm64.tar.gz")
source_armv7h=("https://github.com/zachfi/${_realname}/releases/download/v${pkgver}/${_realname}_${pkgver}_linux_armv7.tar.gz")
#sha256sums=('fa2edae90c7999a6f667bba26a6c63c7165647f77c02c83860237c6d08ee4bbd')
sha256sums_x86_64=('27a9a92dcf06403cf151e4402a7ccfc56bdc805c6bdd22600a52fa3a69e8db15')
sha256sums_aarch64=('93921d953fd02fb53b09095bbe0f4bed89a282a426d16f12c7f00e1429c30d8b')
sha256sums_armv7h=('cdbb85f2963106320222735ccc3970685f26707c89d1c7d644ad4f8400ebee8c')

package() {
	case "$CARCH" in
	arm64)
		_pkgarch="arm64"
		;;
	armv*)
		_pkgarch="arm"
		;;
	x86_64)
		_pkgarch="amd64"
		;;
	esac

	install -Dm755 "${_realname}" "${pkgdir}/usr/bin/${pkgname}"
	install -Dm644 contrib/arch/nodemanager.service "${pkgdir}/usr/lib/systemd/system/nodemanager.service"
}
