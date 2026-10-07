// Copy-to-clipboard for code blocks.
document.querySelectorAll('.code .copy').forEach(function (btn) {
  btn.addEventListener('click', function () {
    var code = btn.parentElement.querySelector('code');
    if (!code || !navigator.clipboard) { return; }
    navigator.clipboard.writeText(code.textContent).then(function () {
      btn.textContent = 'Copied';
      setTimeout(function () { btn.textContent = 'Copy'; }, 1500);
    });
  });
});
