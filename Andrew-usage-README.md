Andrew-usage-README.md

./yoloai new --profile r-dev\# set a profile with a Dockerfile in it to install necessary deps
 add-nested --cpus 4 --memory 8g\# ensure sufficient resources, container won't OOM but rather swap and grind.
 \# Must destroy / recreate increase resources
  --backend apple\#needs to be passed in repeatedly, doesn't seem to honor the yaml config
   ~/opensource/pkbr-nlme:rw\#rw mount -- suitable if you have a git remote you can restore against in case the agent goes haywire
   -d  ./fakedata:r=~/opensource/pkbr-nlme/data #replace a data directory in the container with something else