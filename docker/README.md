Tools for building the base image for our lambda function.

/bash is currently bash-5.2.21, compiled on Amazon Linux 2023, with
`./configure bash_cv_dev_fd=whacky`. This is necessary because the Lambda
sandbox does not mount /devfs, and we need Bash to use /proc/self/fd instead.
